<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Badge as ABadge,
  Button as AButton,
  Input as AInput,
  Modal as AModal,
  Popconfirm as APopconfirm,
  Segmented as ASegmented,
  Select as ASelect,
  SelectOption as ASelectOption,
  Slider as ASlider,
  Space as ASpace,
  Switch as ASwitch,
  Tag as ATag,
  Textarea as ATextarea,
} from "ant-design-vue";
import {
  PhArrowsClockwise,
  PhBrain,
  PhBroadcast,
  PhCheckCircle,
  PhFilmSlate,
  PhHandPalm,
  PhHeart,
  PhInfo,
  PhLightning,
  PhMagicWand,
  PhMegaphone,
  PhMicrophone,
  PhMicrophoneSlash,
  PhMusicNotes,
  PhPaperPlaneTilt,
  PhPencilSimple,
  PhPlay,
  PhPlug,
  PhPlus,
  PhShoppingBag,
  PhSparkle,
  PhSpeakerHigh,
  PhSpeakerSlash,
  PhStop,
  PhTag,
  PhTrash,
  PhUploadSimple,
  PhUserCircle,
  PhVideoCamera,
  PhWarningCircle,
  PhWaveform,
  PhX,
} from "@phosphor-icons/vue";
import ColabOnePrivacyInput from "./ColabOnePrivacyInput.vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import ColabOneSecretInput from "./ColabOneSecretInput.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { colabOneCommand } from "./api";
import {
  buildColabOnePrivacyContext,
  colabOnePrivacyActive,
} from "./colabOnePrivacy";
import {
  COLABONE_LIVE_AUDIO_OUT_SAMPLE_RATE,
  ColabOneLiveAudioPlayer,
  ColabOneLiveMicCapture,
} from "./colabOneLiveAudio";
import {
  ColabOneLiveSessionClient,
  ColabOneLiveVideoRenderer,
  colabOneBuildLiveWsUrl,
  type ColabOneLiveConnectionState,
  type ColabOneLiveTranscriptEvent,
} from "./colabOneLivePlayer";
import type { ColabOneToastKind } from "./colabOneToast";
import { t, tb } from "./i18n";
import type {
  ColabOneLiveActionSpec,
  ColabOneLiveAvatarManifest,
  ColabOneLiveDirectorMode,
  ColabOneLiveDirectorState,
  ColabOneLiveDrivingMotionSpec,
  ColabOneLiveLlmMode,
  ColabOneLiveMicMode,
  ColabOneLiveProductItem,
  ColabOneLiveSessionTokenResult,
  ColabOneLiveTelemetry,
  ColabOneLiveTtsEngine,
  ColabOneLiveVisualEngine,
  ColabOneSnapshot,
} from "./types";

const COLABONE_LIVE_LLM_MODE_STORAGE_KEY = "colabone.live.llmMode";
const COLABONE_LIVE_MAX_PORTRAIT_BYTES = 16 * 1024 * 1024;
const COLABONE_LIVE_MAX_CLIP_BYTES = 128 * 1024 * 1024;
const COLABONE_LIVE_PORTRAIT_EXT_RE = /\.(?:png|jpe?g|webp)$/i;
const COLABONE_LIVE_CLIP_EXT_RE = /\.(?:mp4|webm|mov|mkv)$/i;

const props = withDefaults(
  defineProps<{
    state: ColabOneSnapshot;
    loading?: boolean;
    active?: boolean;
  }>(),
  {
    active: true,
  },
);

const emit = defineEmits<{
  notify: [string, ColabOneToastKind?];
  error: [string];
  clearError: [];
  sync: [];
  upgrade: [];
  "open-canvas-tab": [];
}>();

function notify(msg: string, kind: ColabOneToastKind = "success") {
  emit("clearError");
  emit("notify", msg, kind);
}
function reportError(err: unknown) {
  emit("error", String(err));
}

// Runtime & capability readiness
const gpuReady = computed(() => props.state.runtime.state === "ready");
const privacyContext = computed(() => buildColabOnePrivacyContext(props.state));
const liveStatus = computed(() => props.state.runtime.live_studio);
const liveRunning = computed(
  () => gpuReady.value && Boolean(liveStatus.value?.running),
);
const liveStarting = computed(
  () => gpuReady.value && liveStatus.value?.state === "starting",
);
const capabilities = computed(() => props.state.runtime.capabilities ?? []);
const hasLiveCapability = computed(
  () =>
    !gpuReady.value ||
    capabilities.value.length === 0 ||
    capabilities.value.includes("comfyui_live_studio"),
);
const canMutateRemote = computed(
  () => gpuReady.value && hasLiveCapability.value,
);
const canvasStatus = computed(() => props.state.runtime.comfyui?.canvas);
const tunnelReady = computed(
  () =>
    gpuReady.value &&
    canvasStatus.value?.state === "ready" &&
    Boolean(canvasStatus.value?.url),
);
const hasSavedCanvasConfig = computed(
  () =>
    Boolean(props.state.comfy_canvas_settings?.domain?.trim()) &&
    Boolean(props.state.comfy_canvas_settings?.has_token),
);
const canStartLiveTunnel = computed(
  () => tunnelReady.value || hasSavedCanvasConfig.value,
);

// Engine & mode controls
const visualEngine = ref<ColabOneLiveVisualEngine>("flashhead_lite");
const ttsEngine = ref<ColabOneLiveTtsEngine>("qwen3_tts");
const personaliveEnabled = ref(false);
const directorMode = ref<ColabOneLiveDirectorMode>("dialogue");
const micMode = ref<ColabOneLiveMicMode>("vad");
const stageAspect = ref<"9:16" | "16:9">("9:16");

const directorState = ref<ColabOneLiveDirectorState>("IDLE");
const connState = ref<ColabOneLiveConnectionState>("disconnected");
const activeActionId = ref<string>("idle_loop");
const activeCard = ref<ColabOneLiveProductItem | null>(null);
const transcripts = ref<ColabOneLiveTranscriptEvent[]>([]);
const textInput = ref("");

const startingOrStopping = ref(false);
const micActive = ref(false);
const micStarting = ref(false);
const micLevel = ref(0);
const pttPressed = ref(false);
const audioMuted = ref(false);
const audioVolume = ref(100);
const audioPlaying = ref(false);
const audioSuspended = ref(false);
const hasVideoFrame = ref(false);

const liveTelemetry = ref<ColabOneLiveTelemetry>({
  fps: 0,
  ttft_ms: null,
  ttfa_ms: null,
  e2e_ms: null,
  rtt_ms: null,
  vram_used_mib: null,
  vram_total_mib: null,
});

// Canvas / MSE DOM refs
const stageCanvasRef = ref<HTMLCanvasElement | null>(null);
const mseVideoRef = ref<HTMLVideoElement | null>(null);
const portraitInputRef = ref<HTMLInputElement | null>(null);
const clipInputRef = ref<HTMLInputElement | null>(null);
const drivingVideoInputRef = ref<HTMLInputElement | null>(null);
const packPortraitInputRef = ref<HTMLInputElement | null>(null);

// Media & WebSocket engine instances
let sessionClient: ColabOneLiveSessionClient | null = null;
let micCapture: ColabOneLiveMicCapture | null = null;
let audioPlayer: ColabOneLiveAudioPlayer | null = null;
let videoRenderer: ColabOneLiveVideoRenderer | null = null;
let localInitiatedStart = false;
let hydratedRunningStartedAt: number | null = null;
let isUnmounted = false;
let micOpGen = 0;
let lastSessionEpoch = 0;

interface ColabOneLiveEngineSnapshot {
  visualEngine: ColabOneLiveVisualEngine;
  ttsEngine: ColabOneLiveTtsEngine;
  personaliveEnabled: boolean;
  selectedAvatarId: string;
}
let pendingEngineRollback: ColabOneLiveEngineSnapshot | null = null;
let pendingActionTrigger: {
  id: string;
  label: string;
  prevActionId: string;
} | null = null;

// Avatars, driving motions, actions & products
const remoteAvatars = computed<ColabOneLiveAvatarManifest[]>(
  () => props.state.live_avatars ?? [],
);
const drivingMotions = computed<ColabOneLiveDrivingMotionSpec[]>(
  () => props.state.live_driving_motions ?? [],
);
const localDraftProducts = ref<ColabOneLiveProductItem[]>([]);
const localPortraitPreviews = ref<Record<string, string>>({});
const selectedAvatarId = ref("");
const pendingBuildAvatarId = ref("");

const activeAvatar = computed<ColabOneLiveAvatarManifest | null>(() => {
  const list = remoteAvatars.value;
  if (!list.length || !selectedAvatarId.value) return null;
  return list.find((a) => a.id === selectedAvatarId.value) ?? null;
});

const runningBuildPackTasks = computed(() => {
  if (props.state.runtime.state === "stopped") return [];
  return (props.state.runtime.tasks ?? []).filter(
    (task) => task.state === "running" && task.kind === "live_build_pack",
  );
});

watch(
  [remoteAvatars, runningBuildPackTasks, () => props.state.runtime.state],
  ([list, runningTasks, runtimeState]) => {
    if (runtimeState === "stopped") {
      pendingBuildAvatarId.value = "";
    }
    const pendingId = pendingBuildAvatarId.value;
    if (pendingId) {
      const hasMatchingRunningTask = runningTasks.some(
        (task) => !task.model_id || task.model_id === pendingId,
      );
      if (list.some((a) => a.id === pendingId)) {
        if (!hasMatchingRunningTask) {
          selectedAvatarId.value = pendingId;
          pendingBuildAvatarId.value = "";
        }
      } else if (hasMatchingRunningTask) {
        // Brand-new avatar is actively being built; preserve pendingBuildAvatarId.
        return;
      } else {
        const hasTerminalTask = (props.state.runtime.tasks ?? []).some(
          (task) =>
            task.kind === "live_build_pack" &&
            task.model_id === pendingId &&
            task.state !== "running",
        );
        if (hasTerminalTask) {
          pendingBuildAvatarId.value = "";
        } else {
          // Wait for snapshot poll to reflect newly submitted build-pack task.
          return;
        }
      }
    }

    if (!list.length) {
      selectedAvatarId.value = "";
      return;
    }
    if (localDraftProducts.value.length > 0) {
      localDraftProducts.value = [];
    }
    if (
      !selectedAvatarId.value ||
      !list.some((a) => a.id === selectedAvatarId.value)
    ) {
      const runningId = liveStatus.value?.avatar_id;
      const matchedRunning = runningId
        ? list.find((a) => a.id === runningId)
        : undefined;
      selectedAvatarId.value = matchedRunning?.id ?? list[0].id;
    }
  },
  { immediate: true },
);

const activePortraitPreview = computed(() => {
  const av = activeAvatar.value;
  if (!av) return "";
  return av.portrait_preview_data || localPortraitPreviews.value[av.id] || "";
});

const activeBuildPackTask = computed(() => {
  const running = runningBuildPackTasks.value;
  if (!running.length) return undefined;
  if (pendingBuildAvatarId.value) {
    const byPending = running.find(
      (task) => !task.model_id || task.model_id === pendingBuildAvatarId.value,
    );
    if (byPending) return byPending;
  }
  if (activeAvatar.value?.id) {
    const byActive = running.find(
      (task) => !task.model_id || task.model_id === activeAvatar.value!.id,
    );
    if (byActive) return byActive;
  }
  return running[0];
});

const activeBuildPackProgressPct = computed(() => {
  const task = activeBuildPackTask.value;
  if (!task) return 0;
  const p = Number(task.progress);
  if (!Number.isFinite(p) || p <= 0) return 0;
  return p > 1 ? Math.min(100, Math.round(p)) : Math.round(p * 100);
});

const DEFAULT_ACTIONS = computed<ColabOneLiveActionSpec[]>(() => [
  { id: "idle_loop", label: t("live.actionIdle"), kind: "loop", ready: true },
  { id: "talk_explain", label: t("live.actionTalk"), kind: "loop", ready: true },
  {
    id: "talk_product",
    label: t("live.actionShowcase"),
    kind: "loop",
    ready: true,
  },
  { id: "action_wave", label: t("live.actionWave"), kind: "oneshot", ready: true },
  { id: "action_heart", label: t("live.actionHeart"), kind: "oneshot", ready: true },
  { id: "dance_01", label: t("live.actionDance1"), kind: "dance", ready: true },
]);

const displayActions = computed<ColabOneLiveActionSpec[]>(() => {
  const custom = activeAvatar.value?.actions;
  if (custom && custom.length > 0) return custom;
  return DEFAULT_ACTIONS.value;
});

function productHighlightsList(raw: string | string[] | undefined): string[] {
  if (Array.isArray(raw)) {
    return raw.map((s) => String(s || "").trim()).filter(Boolean);
  }
  if (typeof raw === "string") {
    return raw
      .split(/[\n,，·|]+/)
      .map((s) => s.trim())
      .filter(Boolean);
  }
  return [];
}

function serializeAvatarProducts(
  items: ColabOneLiveProductItem[],
): Array<{
  id: string;
  name: string;
  price: string;
  tag: string;
  highlights: string;
  script_hint: string;
  image_input_id: string;
}> {
  return items.map((item) => ({
    id: item.id,
    name: item.name,
    price: item.price || "",
    tag: item.tag || "",
    highlights: Array.isArray(item.highlights)
      ? item.highlights.join(" · ")
      : String(item.highlights || ""),
    script_hint: item.script_hint || "",
    image_input_id: item.image_input_id || "",
  }));
}

function serializeAvatarActions(actions: ColabOneLiveActionSpec[]) {
  return actions.map((act) => ({
    id: act.id,
    label: act.label || act.name || act.id,
    kind:
      act.kind === "loop" ||
      act.kind === "oneshot" ||
      act.kind === "dance" ||
      act.kind === "personalive_drive"
        ? act.kind
        : "loop",
    clip_path: act.clip_path || "",
    bgm_path: act.bgm_path || "",
    duration_seconds:
      typeof act.duration_seconds === "number" ? act.duration_seconds : 0,
    trigger_keywords: Array.isArray(act.trigger_keywords)
      ? act.trigger_keywords
      : [],
  }));
}

function liveResolutionForAspect(aspect: "9:16" | "16:9"): string {
  return aspect === "16:9" ? "1280x720" : "720x1280";
}

function syncIdleCanvasSize() {
  const canvas = stageCanvasRef.value;
  if (!canvas || hasVideoFrame.value || liveRunning.value) return;
  const targetW = stageAspect.value === "9:16" ? 540 : 960;
  const targetH = stageAspect.value === "9:16" ? 960 : 540;
  if (canvas.width !== targetW) canvas.width = targetW;
  if (canvas.height !== targetH) canvas.height = targetH;
}

watch(stageAspect, () => {
  syncIdleCanvasSize();
});

function isAvatarPackReady(av: ColabOneLiveAvatarManifest | null): boolean {
  if (!av) return false;
  return (av.actions || []).some((a) => Boolean(a.clip_path));
}

const displayProducts = computed<ColabOneLiveProductItem[]>(() => {
  if (activeAvatar.value && Array.isArray(activeAvatar.value.products)) {
    return activeAvatar.value.products;
  }
  return localDraftProducts.value;
});

// LLM Brain settings state with persistent mode selection
function readStoredLlmMode(): ColabOneLiveLlmMode | null {
  try {
    if (typeof window === "undefined") return null;
    const saved = window.localStorage.getItem(COLABONE_LIVE_LLM_MODE_STORAGE_KEY);
    if (saved === "local_gguf" || saved === "external_api") {
      return saved;
    }
  } catch {
    // Ignore storage access errors.
  }
  return null;
}

function writeStoredLlmMode(mode: ColabOneLiveLlmMode): void {
  try {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(COLABONE_LIVE_LLM_MODE_STORAGE_KEY, mode);
    }
  } catch {
    // Ignore storage access errors.
  }
}

const storedLlmMode = readStoredLlmMode();
let llmModeInitialized = storedLlmMode !== null;

const llmMode = ref<ColabOneLiveLlmMode>(storedLlmMode ?? "local_gguf");
const llmBaseUrl = ref("");
const llmModel = ref("");
const llmApiKey = ref("");
const llmHasSavedKey = ref(false);
const llmSaving = ref(false);
const llmClearing = ref(false);
const llmTesting = ref(false);
const llmTestStatus = ref<"idle" | "ok" | "failed">("idle");
const llmTestError = ref("");
const llmDirty = ref(false);

const maskedExternalApiKey = computed(
  () => props.state.live_external_llm?.masked_api_key || "",
);
const canClearExternalLlm = computed(
  () =>
    llmHasSavedKey.value ||
    Boolean(props.state.live_external_llm?.configured) ||
    Boolean(props.state.live_external_llm?.base_url) ||
    Boolean(props.state.live_external_llm?.model),
);

watch(
  () => props.state.live_external_llm,
  (cfg) => {
    if (!cfg) return;
    if (!llmModeInitialized) {
      if (cfg.mode === "local_gguf" || cfg.mode === "external_api") {
        llmMode.value = cfg.mode;
      } else if (cfg.configured) {
        llmMode.value = "external_api";
      }
      llmModeInitialized = true;
    }
    llmHasSavedKey.value = Boolean(cfg.has_api_key);
    if (!llmDirty.value) {
      llmBaseUrl.value = cfg.base_url || "";
      llmModel.value = cfg.model || "";
    }
    if (cfg.last_test_ok === true) {
      llmTestStatus.value = "ok";
      llmTestError.value = "";
    } else if (cfg.last_test_ok === false) {
      llmTestStatus.value = "failed";
      llmTestError.value = cfg.last_test_error || "";
    }
  },
  { immediate: true, deep: true },
);

const loadedLocalModel = computed(() => {
  if (!gpuReady.value) return undefined;
  return props.state.models.find(
    (m) => m.loaded || m.id === props.state.runtime.loaded_model,
  );
});

const loadedLocalModelName = computed(() => {
  if (!gpuReady.value) return "";
  return loadedLocalModel.value?.name || props.state.runtime.loaded_model || "";
});

// VRAM budget indicator (mirrors common/live_studio.py evaluate_colabone_live_vram_budget)
const vramBudgetView = computed(() => {
  const remote = liveStatus.value?.vram_budget;
  const activeCfg = liveStatus.value?.active_config;
  const remoteMatchesSelection =
    liveRunning.value &&
    Boolean(remote) &&
    Boolean(activeCfg) &&
    (activeCfg?.visual_engine ?? liveStatus.value?.visual_engine) ===
      visualEngine.value &&
    (activeCfg?.tts_engine ?? liveStatus.value?.tts_engine) ===
      ttsEngine.value &&
    (activeCfg?.llm_mode ?? liveStatus.value?.llm_mode) === llmMode.value &&
    Boolean(
      activeCfg?.personalive_enabled ?? liveStatus.value?.personalive_enabled,
    ) === personaliveEnabled.value;

  const gpuTotalMib =
    (remoteMatchesSelection
      ? remote?.available_mib || remote?.total_mib
      : 0) ||
    props.state.runtime.gpu?.total_mib ||
    props.state.runtime.resources?.gpu?.total_mib ||
    0;

  let estimatedMib: number;
  let fits: boolean;
  let personaliveMode: string = personaliveEnabled.value
    ? "co_located"
    : "disabled";

  if (remoteMatchesSelection && remote) {
    estimatedMib = remote.total_required_mib || remote.estimated_mib || 0;
    personaliveMode = remote.personalive_mode || personaliveMode;
    fits =
      typeof remote.ok === "boolean"
        ? remote.ok
        : typeof remote.fits === "boolean"
          ? remote.fits
          : gpuTotalMib <= 0 || estimatedMib <= gpuTotalMib;
  } else {
    const visualMib = visualEngine.value === "flashhead_lite" ? 6656 : 7680;
    const ttsMib = 8192;
    const asrMib = 1024;
    const nvencMib = 1024;
    const safetyMib = 3072;
    const loadedLlmMib = loadedLocalModel.value?.min_vram_mib || 6144;
    const llmMib = llmMode.value === "local_gguf" ? loadedLlmMib : 0;
    const plCatalogMib = 12288;

    const baseNoPl =
      asrMib + ttsMib + visualMib + nvencMib + llmMib + safetyMib;
    const coLocatePl = baseNoPl + plCatalogMib;
    const hotSwapPeak =
      asrMib +
      ttsMib +
      Math.max(visualMib, plCatalogMib) +
      nvencMib +
      llmMib +
      safetyMib;

    if (personaliveEnabled.value) {
      if (gpuTotalMib <= 0 || gpuTotalMib >= coLocatePl) {
        personaliveMode = "co_located";
        estimatedMib = coLocatePl;
      } else if (gpuTotalMib >= baseNoPl && gpuTotalMib >= hotSwapPeak) {
        personaliveMode = "hot_swap";
        estimatedMib = hotSwapPeak;
      } else {
        personaliveMode = "unsupported";
        estimatedMib = hotSwapPeak;
      }
    } else {
      personaliveMode = "disabled";
      estimatedMib = baseNoPl;
    }

    fits =
      gpuTotalMib <= 0 ||
      (estimatedMib <= gpuTotalMib && personaliveMode !== "unsupported");
  }

  const estGb = (estimatedMib / 1024).toFixed(1);
  const totalGb = gpuTotalMib > 0 ? (gpuTotalMib / 1024).toFixed(1) : "";
  const isHotSwap = personaliveMode === "hot_swap";
  const tight =
    gpuTotalMib > 0 &&
    fits &&
    (isHotSwap || estimatedMib > gpuTotalMib * 0.82);

  return {
    estGb,
    totalGb,
    fits,
    tight,
    isHotSwap,
    color: !fits ? "error" : tight ? "warning" : "success",
    tooltip: !fits
      ? t("live.vramBudgetOverTooltip")
      : isHotSwap
        ? t("live.vramBudgetHotSwapTooltip")
        : t("live.vramBudgetLabel"),
    label: totalGb
      ? t("live.vramBudgetRatio", { est: estGb, total: totalGb })
      : t("live.vramBudgetEstOnly", { est: estGb }),
  };
});

// Status & badges
const directorStateMeta = computed(() => {
  switch (directorState.value) {
    case "LISTENING":
      return {
        label: t("live.stateListening"),
        color: "processing" as const,
        badge: "processing" as const,
      };
    case "THINKING":
      return {
        label: t("live.stateThinking"),
        color: "warning" as const,
        badge: "warning" as const,
      };
    case "SPEAKING":
      return {
        label: t("live.stateSpeaking"),
        color: "success" as const,
        badge: "success" as const,
      };
    case "PERFORMING":
      return {
        label: t("live.statePerforming"),
        color: "blue" as const,
        badge: "processing" as const,
      };
    default:
      return {
        label: t("live.stateIdle"),
        color: "default" as const,
        badge: "default" as const,
      };
  }
});

const connBadgeMeta = computed(() => {
  switch (connState.value) {
    case "connected":
      return { label: t("live.connConnected"), color: "success" as const };
    case "connecting":
      return { label: t("live.connConnecting"), color: "processing" as const };
    case "authenticating":
      return {
        label: t("live.connAuthenticating"),
        color: "processing" as const,
      };
    case "reconnecting":
      return { label: t("live.connReconnecting"), color: "warning" as const };
    case "error":
      return { label: t("live.connError"), color: "error" as const };
    default:
      return { label: t("live.connDisconnected"), color: "default" as const };
  }
});

const isConnected = computed(() => connState.value === "connected");
const canBargeIn = computed(
  () =>
    isConnected.value &&
    (directorState.value === "SPEAKING" ||
      directorState.value === "PERFORMING" ||
      directorState.value === "THINKING" ||
      audioPlaying.value),
);

const micModeOptions = computed(() => [
  { label: t("live.micModeVad"), value: "vad" },
  { label: t("live.micModePtt"), value: "ptt" },
]);

const directorModeOptions = computed(() => [
  { label: t("live.directorModeDialogue"), value: "dialogue" },
  { label: t("live.directorModeDirector"), value: "director" },
]);

const llmModeOptions = computed(() => [
  { label: t("live.llmModeLocal"), value: "local_gguf" },
  { label: t("live.llmModeExternal"), value: "external_api" },
]);

const visibleTranscripts = computed(() => transcripts.value.slice(-3));

const hudVramText = computed(() => {
  const usedMib =
    liveTelemetry.value.vram_used_mib ?? props.state.runtime.gpu?.used_mib;
  const totalMib =
    liveTelemetry.value.vram_total_mib ?? props.state.runtime.gpu?.total_mib;
  if (usedMib === null || usedMib === undefined) return "--";
  const u = (usedMib / 1024).toFixed(1);
  if (totalMib && totalMib > 0) {
    return `${u}/${(totalMib / 1024).toFixed(0)}G`;
  }
  return `${u}G`;
});

function formatMs(val: number | null | undefined): string {
  if (val === null || val === undefined || !Number.isFinite(val) || val < 0) {
    return "--";
  }
  return `${Math.round(val)}ms`;
}

function hydrateControlsFromServer(raw: Record<string, unknown> | null | undefined) {
  if (!raw) return;
  if (raw.visual_engine === "flashhead_lite" || raw.visual_engine === "musetalk_bank") {
    visualEngine.value = raw.visual_engine;
  }
  if (raw.tts_engine === "qwen3_tts" || raw.tts_engine === "voxcpm2") {
    ttsEngine.value = raw.tts_engine;
  }
  if (typeof raw.personalive_enabled === "boolean") {
    personaliveEnabled.value = raw.personalive_enabled;
  }
  if (raw.director_mode === "dialogue" || raw.director_mode === "director") {
    directorMode.value = raw.director_mode;
  }
  if (raw.mic_mode === "vad" || raw.mic_mode === "ptt") {
    micMode.value = raw.mic_mode;
    micCapture?.setMode(raw.mic_mode);
  }
  if (
    typeof raw.avatar_id === "string" &&
    raw.avatar_id &&
    remoteAvatars.value.some((a) => a.id === raw.avatar_id)
  ) {
    selectedAvatarId.value = raw.avatar_id;
  }
  if (typeof raw.resolution === "string") {
    const [wRaw, hRaw] = raw.resolution.split("x");
    const w = Number(wRaw);
    const h = Number(hRaw);
    if (Number.isFinite(w) && Number.isFinite(h) && w > 0 && h > 0) {
      stageAspect.value = w >= h ? "16:9" : "9:16";
    }
  }
}

// Sync snapshot live_studio fields into local UI state (non-destructive)
watch(
  liveStatus,
  (st) => {
    if (!st) return;
    if (st.director_state && !isConnected.value) {
      directorState.value = st.director_state;
    }
    if (st.active_action_id && !isConnected.value) {
      activeActionId.value = st.active_action_id;
    }
    if (st.telemetry) {
      const merged: ColabOneLiveTelemetry = {
        ...liveTelemetry.value,
        ...st.telemetry,
        fps:
          typeof st.telemetry.fps === "number" && st.telemetry.fps > 0
            ? st.telemetry.fps
            : liveTelemetry.value.fps,
      };
      liveTelemetry.value = merged;
    }
    // Hydrate running config when discovering an already-running remote session
    if (
      st.running &&
      !isConnected.value &&
      !startingOrStopping.value &&
      hydratedRunningStartedAt !== (st.started_at ?? -1)
    ) {
      hydratedRunningStartedAt = st.started_at ?? -1;
      hydrateControlsFromServer(
        (st.active_config as Record<string, unknown> | null) ??
          (st as unknown as Record<string, unknown>),
      );
    }
    if (!st.running) {
      hydratedRunningStartedAt = null;
    }
  },
  { deep: true, immediate: true },
);

// Cleanly tear down media session ONLY when GPU disconnects or running transitions true to false
watch(
  [gpuReady, liveRunning],
  ([nowGpuReady, nowRunning], [, prevRunning]) => {
    if (!nowGpuReady && (sessionClient || micActive.value || micStarting.value)) {
      teardownMediaSession();
      return;
    }
    if (
      prevRunning &&
      !nowRunning &&
      sessionClient &&
      !startingOrStopping.value
    ) {
      teardownMediaSession();
    }
  },
);

// Tab active lifecycle: release PTT and stop mic immediately when leaving Live Studio tab
watch(
  () => props.active,
  (isActive) => {
    if (isActive === false) {
      setPttHolding(false);
      stopMicCapture();
    }
  },
);

// Audio/Video & WSS session lifecycle
function syncAudioSuspendedState() {
  audioSuspended.value =
    Boolean(audioPlayer) &&
    audioPlayer?.getAudioState() === "suspended" &&
    (isConnected.value || liveRunning.value);
}

function resumeSuspendedAudio() {
  if (!audioPlayer) return;
  if (audioPlayer.getAudioState() === "suspended") {
    void audioPlayer
      .resume()
      .then(syncAudioSuspendedState)
      .catch(syncAudioSuspendedState);
  } else {
    syncAudioSuspendedState();
  }
}

function ensureVideoRenderer(
  codec = "h264",
  h264CodecString?: string,
): ColabOneLiveVideoRenderer | null {
  const canvas = stageCanvasRef.value;
  if (!canvas) return null;
  if (!videoRenderer) {
    videoRenderer = new ColabOneLiveVideoRenderer({
      canvas,
      videoElement: mseVideoRef.value,
      codec,
      h264CodecString,
      onFrameRendered: ({ fps }) => {
        hasVideoFrame.value = true;
        liveTelemetry.value = { ...liveTelemetry.value, fps };
      },
      onError: (err) => {
        reportError(err.message);
      },
    });
  } else {
    videoRenderer.setCodec(codec, h264CodecString);
  }
  return videoRenderer;
}

function ensureAudioPlayer(): ColabOneLiveAudioPlayer {
  if (!audioPlayer) {
    audioPlayer = new ColabOneLiveAudioPlayer({
      sampleRate: COLABONE_LIVE_AUDIO_OUT_SAMPLE_RATE,
      onPlaybackStateChange: (playing) => {
        audioPlaying.value = playing;
      },
    });
    audioPlayer.setVolume(audioVolume.value / 100);
    audioPlayer.setMuted(audioMuted.value);
  }
  return audioPlayer;
}

function captureEngineSnapshot(): ColabOneLiveEngineSnapshot {
  return {
    visualEngine: visualEngine.value,
    ttsEngine: ttsEngine.value,
    personaliveEnabled: personaliveEnabled.value,
    selectedAvatarId: selectedAvatarId.value,
  };
}

function restoreEngineSnapshot() {
  if (!pendingEngineRollback) return;
  const snap = pendingEngineRollback;
  pendingEngineRollback = null;
  visualEngine.value = snap.visualEngine;
  ttsEngine.value = snap.ttsEngine;
  personaliveEnabled.value = snap.personaliveEnabled;
  selectedAvatarId.value = snap.selectedAvatarId;
}

function formatLiveWsError(msg: string, code?: string): string {
  switch (code) {
    case "canvas_not_ready":
      return t("live.errWsCanvasNotReady");
    case "empty_token":
      return t("live.errWsEmptyToken");
    case "invalid_ws_url":
      return t("live.errWsInvalidUrl");
    case "ws_error":
      return t("live.errWsError");
    case "ws_close_4401":
    case "unauthorized":
      return t("live.errWsUnauthorized");
    case "reconnect_exhausted":
      return t("live.errWsReconnectExhausted");
    case "personalive_disabled":
      return t("live.errWsPersonaLiveDisabled");
    case "switch_engine_failed":
    case "invalid_engine": {
      const detail = msg ? tb(msg) : "";
      return detail
        ? `${t("live.errWsSwitchEngineFailed")}: ${detail}`
        : t("live.errWsSwitchEngineFailed");
    }
    default:
      return tb(msg);
  }
}

async function fetchSessionToken(): Promise<ColabOneLiveSessionTokenResult> {
  const res = await colabOneCommand<ColabOneLiveSessionTokenResult>(
    "comfyui.live.session_token",
    {},
  );
  let wsUrl = String(res?.ws_url || "").trim();
  if (
    !wsUrl &&
    res?.canvas_ready !== false &&
    canvasStatus.value?.state === "ready" &&
    canvasStatus.value?.url
  ) {
    wsUrl = colabOneBuildLiveWsUrl(
      canvasStatus.value.url,
      res?.ws_path || liveStatus.value?.ws_path || "/_colabone_live/ws",
    );
  }
  return {
    ...res,
    ws_url: wsUrl,
  };
}

async function connectLiveSession(opts?: { localInitiated?: boolean }) {
  if (isUnmounted) return;
  teardownMediaSession({ preserveAudioPlayer: true });
  localInitiatedStart = Boolean(opts?.localInitiated);
  const player = ensureAudioPlayer();
  // Non-blocking AudioContext resume so suspended autoplay never stalls WebSocket connect.
  void player
    .resume()
    .then(syncAudioSuspendedState)
    .catch(syncAudioSuspendedState);

  ensureVideoRenderer(liveStatus.value?.codec || "h264");

  sessionClient = new ColabOneLiveSessionClient({
    fetchToken: fetchSessionToken,
    shouldRetryCanvasNotReady: () =>
      gpuReady.value &&
      !isUnmounted &&
      (canvasStatus.value?.state === "starting" ||
        hasSavedCanvasConfig.value ||
        liveStarting.value ||
        startingOrStopping.value),
    onConnectionState: (st, detail) => {
      connState.value = st;
      if (st === "reconnecting" && detail === "canvas_starting") {
        emit("sync");
      }
      syncAudioSuspendedState();
    },
    onAuthOk: (payload) => {
      const serverEpoch =
        typeof payload.epoch === "number" &&
        Number.isFinite(payload.epoch) &&
        payload.epoch >= 0
          ? Math.floor(payload.epoch)
          : null;
      const prevAudioEpoch = audioPlayer?.getCurrentEpoch() ?? 0;
      const epochDecreased =
        serverEpoch !== null &&
        (serverEpoch < lastSessionEpoch || serverEpoch < prevAudioEpoch);
      if (serverEpoch !== null) {
        lastSessionEpoch = serverEpoch;
      }

      const codec =
        typeof payload.codec === "string"
          ? payload.codec
          : sessionClient?.getNegotiatedCodec() || "h264";
      const h264Codec =
        typeof payload.h264_codec === "string" ? payload.h264_codec : undefined;
      const activeRenderer = ensureVideoRenderer(codec, h264Codec);
      if (epochDecreased && serverEpoch !== null) {
        audioPlayer?.resetEpoch(serverEpoch);
        activeRenderer?.resetEpoch();
      }
      if (localInitiatedStart) {
        sessionClient?.sendSetMode(directorMode.value, micMode.value);
      } else {
        hydrateControlsFromServer(payload);
      }
      localInitiatedStart = false;
      syncAudioSuspendedState();
    },
    onState: (st, detail) => {
      directorState.value = st;
      if (typeof detail?.action_id === "string" && detail.action_id) {
        activeActionId.value = detail.action_id;
      }
      if (
        detail &&
        ("visual_engine" in detail ||
          "tts_engine" in detail ||
          "personalive_enabled" in detail ||
          "avatar_id" in detail)
      ) {
        pendingEngineRollback = null;
        hydrateControlsFromServer(detail);
        emit("sync");
      }
    },
    onAction: (actionId) => {
      activeActionId.value = actionId;
      if (pendingActionTrigger && pendingActionTrigger.id === actionId) {
        const label = pendingActionTrigger.label;
        pendingActionTrigger = null;
        notify(t("live.noticeActionTriggered", { name: label }), "info");
      }
    },
    onBargeIn: (epoch) => {
      if (epoch > lastSessionEpoch) {
        lastSessionEpoch = epoch;
      }
      ensureAudioPlayer().interrupt(epoch);
      videoRenderer?.resetEpoch();
    },
    onTranscript: (entry) => {
      const appendTranscriptChunk = (prev: string, next: string) => {
        const needsSpace =
          Boolean(prev) &&
          !/[\s，。！？；：、]$/.test(prev) &&
          /^[A-Za-z0-9"'(\[{]/.test(next);
        return `${prev}${needsSpace ? " " : ""}${next}`;
      };
      const list = transcripts.value.slice();
      const idx = list.findIndex((item) => item.id === entry.id);
      if (idx >= 0 && entry.isDelta && !list[idx].isFinal) {
        list[idx] = {
          ...list[idx],
          text: appendTranscriptChunk(list[idx].text, entry.text),
          isFinal: entry.isFinal,
          ts: entry.ts,
        };
      } else if (idx >= 0) {
        list[idx] = entry;
      } else if (entry.isDelta) {
        const last = list[list.length - 1];
        if (
          last &&
          !last.isFinal &&
          last.epoch === entry.epoch &&
          last.role === entry.role
        ) {
          list[list.length - 1] = {
            ...last,
            text: appendTranscriptChunk(last.text, entry.text),
            isFinal: entry.isFinal,
            ts: entry.ts,
          };
        } else {
          list.push(entry);
        }
      } else {
        list.push(entry);
      }
      transcripts.value = list.slice(-24);
    },
    onCard: (card) => {
      if (!card) {
        activeCard.value = null;
        return;
      }
      const hasContent =
        Boolean(card.name?.trim()) ||
        Boolean(card.price?.trim()) ||
        productHighlightsList(card.highlights).length > 0;
      if (!hasContent && card.id) {
        const matched = displayProducts.value.find((p) => p.id === card.id);
        activeCard.value = matched ?? null;
        return;
      }
      if (!hasContent) {
        activeCard.value = null;
        return;
      }
      activeCard.value = card;
    },
    onTelemetry: (tel) => {
      const next: ColabOneLiveTelemetry = {
        ...liveTelemetry.value,
        ...tel,
        fps:
          typeof tel.fps === "number" ? tel.fps : liveTelemetry.value.fps,
      };
      liveTelemetry.value = next;
      if (tel.video_codec) {
        ensureVideoRenderer(tel.video_codec);
      }
    },
    onPong: (_ts, rttMs) => {
      if (typeof rttMs === "number") {
        liveTelemetry.value = {
          ...liveTelemetry.value,
          rtt_ms: rttMs,
        };
      }
    },
    onVideoFrame: (frame) => {
      if (frame.epoch > lastSessionEpoch) {
        lastSessionEpoch = frame.epoch;
      }
      const r = ensureVideoRenderer(
        sessionClient?.getNegotiatedCodec() || "h264",
      );
      r?.pushVideoFrame(frame.payload, frame.ptsMs);
    },
    onAudioFrame: (frame) => {
      if (frame.epoch > lastSessionEpoch) {
        lastSessionEpoch = frame.epoch;
      }
      const byteLen = frame.payload.byteLength;
      if (byteLen < 2) return;
      const evenLen = byteLen - (byteLen % 2);
      const aligned =
        frame.payload.byteOffset % 2 === 0
          ? new Int16Array(
              frame.payload.buffer,
              frame.payload.byteOffset,
              evenLen / 2,
            )
          : new Int16Array(frame.payload.slice(0, evenLen).buffer);
      const p = ensureAudioPlayer();
      p.enqueue(frame.epoch, frame.ptsMs, aligned);
      syncAudioSuspendedState();
    },
    onFmp4Chunk: (frame) => {
      if (frame.epoch > lastSessionEpoch) {
        lastSessionEpoch = frame.epoch;
      }
      const r = ensureVideoRenderer("fmp4");
      r?.pushFmp4Chunk(frame.payload);
    },
    onError: (msg, code) => {
      if (code === "ws_error" && connState.value === "reconnecting") {
        return;
      }
      if (code === "switch_engine_failed" || code === "invalid_engine") {
        restoreEngineSnapshot();
      }
      if (
        code === "personalive_disabled" ||
        code === "invalid_action" ||
        code === "driving_not_found" ||
        code === "driving_failed"
      ) {
        if (pendingActionTrigger) {
          activeActionId.value = pendingActionTrigger.prevActionId || "idle_loop";
          pendingActionTrigger = null;
        } else if (
          (code === "personalive_disabled" ||
            code === "driving_not_found" ||
            code === "driving_failed") &&
          drivingMotions.value.some((m) => m.id === activeActionId.value)
        ) {
          activeActionId.value = "idle_loop";
        }
      }
      reportError(formatLiveWsError(msg, code));
    },
  });

  await sessionClient.connect();
  if (isUnmounted) {
    teardownMediaSession();
    return;
  }
}

function teardownMediaSession(opts?: {
  preserveAudioPlayer?: boolean;
  preserveLocalInitiated?: boolean;
}) {
  if (!opts?.preserveLocalInitiated) {
    localInitiatedStart = false;
  }
  pendingEngineRollback = null;
  pendingActionTrigger = null;
  lastSessionEpoch = 0;
  stopMicCapture();
  if (sessionClient) {
    sessionClient.disconnect();
    sessionClient = null;
  }
  if (audioPlayer) {
    if (opts?.preserveAudioPlayer) {
      audioPlayer.resetEpoch(0);
    } else {
      audioPlayer.destroy();
      audioPlayer = null;
    }
  }
  if (videoRenderer) {
    videoRenderer.destroy();
    videoRenderer = null;
  }
  audioPlaying.value = false;
  if (!opts?.preserveAudioPlayer) {
    audioSuspended.value = false;
  }
  hasVideoFrame.value = false;
  connState.value = "disconnected";
  directorState.value = "IDLE";
  liveTelemetry.value = {
    ...liveTelemetry.value,
    fps: 0,
    rtt_ms: null,
  };
}

async function startLiveStudio() {
  if (startingOrStopping.value) return;
  // Unlock Web Audio synchronously within user click gesture before any await.
  void ensureAudioPlayer()
    .resume()
    .then(syncAudioSuspendedState)
    .catch(syncAudioSuspendedState);

  startingOrStopping.value = true;
  emit("clearError");
  try {
    if (llmMode.value === "external_api" && llmDirty.value) {
      const saved = await persistExternalLlmSettings(false);
      if (isUnmounted || !saved) {
        return;
      }
    }

    const startPayload: Record<string, unknown> = {
      visual_engine: visualEngine.value,
      tts_engine: ttsEngine.value,
      llm_mode: llmMode.value,
      personalive_enabled: personaliveEnabled.value,
      director_mode: directorMode.value,
      mic_mode: micMode.value,
      resolution: liveResolutionForAspect(stageAspect.value),
      target_fps: 25,
    };
    const validAvatar = remoteAvatars.value.find(
      (a) => a.id === selectedAvatarId.value,
    );
    if (validAvatar) {
      startPayload.avatar_id = validAvatar.id;
    }
    if (llmMode.value === "local_gguf" && props.state.runtime.loaded_model) {
      startPayload.local_llm_model_id = props.state.runtime.loaded_model;
    }
    await colabOneCommand("comfyui.live.start", startPayload);
    if (isUnmounted) return;
    notify(t("live.noticeStarted"), "info");
    emit("sync");
    await connectLiveSession({ localInitiated: true });
    if (isUnmounted) return;
  } catch (err) {
    localInitiatedStart = false;
    if (!isUnmounted) {
      reportError(err);
    }
  } finally {
    startingOrStopping.value = false;
  }
}

async function stopLiveStudio() {
  if (startingOrStopping.value) return;
  startingOrStopping.value = true;
  emit("clearError");
  try {
    teardownMediaSession();
    await colabOneCommand("comfyui.live.stop");
    notify(t("live.noticeStopped"), "success");
    emit("sync");
  } catch (err) {
    reportError(err);
  } finally {
    startingOrStopping.value = false;
  }
}

// Engine live switching
function onVisualEngineChange(val: unknown) {
  const next = val === "musetalk_bank" ? "musetalk_bank" : "flashhead_lite";
  if (sessionClient?.isConnected()) {
    if (!pendingEngineRollback) {
      pendingEngineRollback = captureEngineSnapshot();
    }
    visualEngine.value = next;
    sessionClient.sendSwitchEngine({ visual_engine: next });
    return;
  }
  visualEngine.value = next;
}

function onTtsEngineChange(val: unknown) {
  const next = val === "voxcpm2" ? "voxcpm2" : "qwen3_tts";
  if (sessionClient?.isConnected()) {
    if (!pendingEngineRollback) {
      pendingEngineRollback = captureEngineSnapshot();
    }
    ttsEngine.value = next;
    sessionClient.sendSwitchEngine({ tts_engine: next });
    return;
  }
  ttsEngine.value = next;
}

function onPersonaLiveChange(checked: boolean | string | number) {
  const next = Boolean(checked);
  if (sessionClient?.isConnected()) {
    if (!pendingEngineRollback) {
      pendingEngineRollback = captureEngineSnapshot();
    }
    personaliveEnabled.value = next;
    sessionClient.sendSwitchEngine({ personalive_enabled: next });
    return;
  }
  personaliveEnabled.value = next;
}

function onAvatarSelect(val: unknown) {
  const next = String(val || "");
  if (sessionClient?.isConnected() && next) {
    if (!pendingEngineRollback) {
      pendingEngineRollback = captureEngineSnapshot();
    }
    selectedAvatarId.value = next;
    sessionClient.sendSwitchEngine({ avatar_id: next });
    return;
  }
  selectedAvatarId.value = next;
}

async function deleteSelectedAvatar() {
  const av = activeAvatar.value;
  if (!av || !canMutateRemote.value) return;
  uploadingAsset.value = true;
  try {
    await colabOneCommand("comfyui.live.avatars.delete", {
      avatar_id: av.id,
      id: av.id,
    });
    const nextPreviews = { ...localPortraitPreviews.value };
    delete nextPreviews[av.id];
    localPortraitPreviews.value = nextPreviews;
    localDraftProducts.value = [];
    if (
      activeCard.value &&
      (av.products ?? []).some((p) => p.id === activeCard.value!.id)
    ) {
      activeCard.value = null;
      if (sessionClient?.isConnected()) {
        sessionClient.sendSetCard(null);
      }
    }
    const remaining = remoteAvatars.value.filter((a) => a.id !== av.id);
    const nextId = remaining[0]?.id ?? "";
    selectedAvatarId.value = nextId;
    if (nextId && sessionClient?.isConnected()) {
      sessionClient.sendSwitchEngine({ avatar_id: nextId });
    }
    notify(t("live.noticeAvatarDeleted"), "success");
    emit("sync");
  } catch (err) {
    reportError(err);
  } finally {
    uploadingAsset.value = false;
  }
}

// Microphone & PTT controls
async function toggleMic() {
  if (micStarting.value) return;
  if (micActive.value) {
    stopMicCapture();
    return;
  }
  const gen = ++micOpGen;
  micStarting.value = true;
  let errorReported = false;
  const capture = new ColabOneLiveMicCapture({
    mode: micMode.value,
    onPcmFrame: (pcm) => {
      sessionClient?.sendMicPcm(pcm);
    },
    onLevel: (rms) => {
      micLevel.value = Math.min(1, Math.round(rms * 320) / 100);
    },
    onError: (err) => {
      if (gen !== micOpGen || isUnmounted) return;
      errorReported = true;
      reportError(err.message);
    },
  });
  micCapture = capture;
  try {
    await capture.start();
    if (gen !== micOpGen || isUnmounted || props.active === false) {
      capture.stop();
      if (micCapture === capture) {
        micCapture = null;
      }
      return;
    }
    micActive.value = true;
  } catch (err) {
    capture.stop();
    if (micCapture === capture) {
      micCapture = null;
    }
    if (!errorReported && gen === micOpGen && !isUnmounted) {
      reportError(err);
    }
  } finally {
    if (gen === micOpGen) {
      micStarting.value = false;
    }
  }
}

function stopMicCapture() {
  micOpGen += 1;
  micStarting.value = false;
  if (pttPressed.value) {
    pttPressed.value = false;
    sessionClient?.sendPttState(false);
  }
  if (micCapture) {
    micCapture.stop();
    micCapture = null;
  }
  micActive.value = false;
  micLevel.value = 0;
}

function onMicModeChange(val: string | number) {
  const next: ColabOneLiveMicMode = val === "ptt" ? "ptt" : "vad";
  micMode.value = next;
  if (pttPressed.value) {
    pttPressed.value = false;
    sessionClient?.sendPttState(false);
  }
  micCapture?.setMode(next);
  if (sessionClient?.isConnected()) {
    sessionClient.sendSetMode(directorMode.value, next);
  }
}

function onDirectorModeChange(val: string | number) {
  const next: ColabOneLiveDirectorMode =
    val === "director" ? "director" : "dialogue";
  directorMode.value = next;
  if (sessionClient?.isConnected()) {
    sessionClient.sendSetMode(next, micMode.value);
  }
}

function setPttHolding(active: boolean) {
  if (active && (micMode.value !== "ptt" || !micActive.value)) return;
  if (pttPressed.value === active) return;
  pttPressed.value = active;
  micCapture?.setPttActive(active);
  sessionClient?.sendPttState(active);
}

function releasePttIfActive() {
  if (pttPressed.value) {
    setPttHolding(false);
  }
}

function onVisibilityChange() {
  if (typeof document !== "undefined" && document.hidden) {
    releasePttIfActive();
  }
}

function hasVisibleAntOverlay(): boolean {
  if (typeof document === "undefined") return false;
  const overlays = document.querySelectorAll<HTMLElement>(
    [
      ".ant-modal-wrap",
      ".ant-drawer-open",
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

function isInteractiveOrEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName.toLowerCase();
  if (
    tag === "input" ||
    tag === "textarea" ||
    tag === "select" ||
    tag === "button" ||
    tag === "a" ||
    tag === "summary"
  ) {
    return true;
  }
  if (target.isContentEditable) return true;
  return Boolean(
    target.closest(
      "[contenteditable='true'], button, a, select, summary, [role='button'], [role='switch'], [role='slider'], [role='tab'], [role='combobox'], [role='listbox'], [role='option'], [role='radio'], [role='checkbox'], [role='menuitem'], .ant-select, .ant-segmented",
    ),
  );
}

function onGlobalKeyDown(event: KeyboardEvent) {
  if (props.active !== false && audioSuspended.value) {
    resumeSuspendedAudio();
  }
  if (event.code !== "Space" || event.repeat) return;
  if (props.active === false) return;
  if (buildPackModalOpen.value || productModalOpen.value) return;
  if (hasVisibleAntOverlay()) return;
  if (micMode.value !== "ptt" || !micActive.value) return;
  if (isInteractiveOrEditableTarget(event.target)) return;
  event.preventDefault();
  setPttHolding(true);
}

function onGlobalKeyUp(event: KeyboardEvent) {
  if (event.code !== "Space") return;
  if (!pttPressed.value) return;
  event.preventDefault();
  setPttHolding(false);
}

function triggerBargeIn() {
  if (!sessionClient?.isConnected()) return;
  const nextEpoch = sessionClient.sendBargeIn("user_button");
  notify(t("live.noticeBargeIn", { epoch: nextEpoch }), "info");
}

function sendDirectiveText() {
  const content = textInput.value.trim();
  if (!content) return;
  if (!sessionClient?.isConnected()) {
    reportError(t("live.connDisconnected"));
    return;
  }
  const ok = sessionClient.sendTextInput(content, directorMode.value);
  if (ok) {
    transcripts.value = [
      ...transcripts.value.slice(-23),
      {
        id: `local-${Date.now()}`,
        role: directorMode.value === "director" ? "director" : "user",
        text: content,
        isFinal: true,
        epoch: sessionClient.getCurrentEpoch(),
        ts: Date.now(),
      },
    ];
    textInput.value = "";
  }
}

// Output speaker controls
function toggleAudioMute() {
  audioMuted.value = !audioMuted.value;
  audioPlayer?.setMuted(audioMuted.value);
}

function onVolumeChange(val: number | [number, number]) {
  const num = Array.isArray(val) ? val[0] : val;
  audioVolume.value = num;
  if (num > 0 && audioMuted.value) {
    audioMuted.value = false;
    audioPlayer?.setMuted(false);
  }
  audioPlayer?.setVolume(num / 100);
}

// Action wall & PersonaLive driving triggers
function triggerAction(action: ColabOneLiveActionSpec) {
  if (!sessionClient?.isConnected()) return;
  if (action.kind === "personalive_drive" && !personaliveEnabled.value) return;
  pendingActionTrigger = {
    id: action.id,
    label: action.label || action.name || action.id,
    prevActionId: activeActionId.value,
  };
  sessionClient.sendTriggerAction(
    action.id,
    action.kind === "personalive_drive"
      ? {
          kind: "personalive_drive",
          driving_motion_id: action.id,
          loop: false,
        }
      : {
          loop: action.kind === "loop",
        },
  );
}

function triggerDrivingMotion(motion: ColabOneLiveDrivingMotionSpec) {
  if (!sessionClient?.isConnected() || !personaliveEnabled.value) return;
  pendingActionTrigger = {
    id: motion.id,
    label: motion.label || motion.id,
    prevActionId: activeActionId.value,
  };
  sessionClient.sendTriggerAction(motion.id, {
    kind: "personalive_drive",
    driving_motion_id: motion.id,
  });
}

async function deleteDrivingMotion(motion: ColabOneLiveDrivingMotionSpec) {
  if (!canMutateRemote.value) return;
  uploadingAsset.value = true;
  try {
    await colabOneCommand("comfyui.live.driving.delete", {
      id: motion.id,
      motion_id: motion.id,
    });
    notify(t("live.noticeDrivingDeleted"), "success");
    emit("sync");
  } catch (err) {
    reportError(err);
  } finally {
    uploadingAsset.value = false;
  }
}

// File upload helpers (Portrait / Action Clip / Driving Video / BGM limit)
function validateLiveFileSize(
  file: File,
  maxBytes: number,
  kindLabel: string,
): boolean {
  if (file.size > maxBytes) {
    reportError(
      t("live.errFileTooLarge", {
        kind: kindLabel,
        maxMb: Math.round(maxBytes / (1024 * 1024)),
      }),
    );
    return false;
  }
  return true;
}

function readFileAsBase64(
  file: File,
): Promise<{ dataUrl: string; base64: string }> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const dataUrl = typeof reader.result === "string" ? reader.result : "";
      const comma = dataUrl.indexOf(",");
      const base64 = comma >= 0 ? dataUrl.slice(comma + 1) : dataUrl;
      resolve({ dataUrl, base64 });
    };
    reader.onerror = () => reject(new Error("Failed to read selected file"));
    reader.readAsDataURL(file);
  });
}

const uploadingAsset = ref(false);

function makeSafeLiveId(prefix: string, fallback = "item"): string {
  const cleaned = prefix
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "_")
    .replace(/^[^a-z0-9]+|[_-]+$/g, "")
    .slice(0, 32);
  return cleaned || `${fallback}_${Date.now()}`;
}

function makeUniqueAvatarId(name: string): string {
  const base = makeSafeLiveId(name, "avatar");
  if (!remoteAvatars.value.some((a) => a.id === base)) {
    return base;
  }
  return `${base.slice(0, 24)}_${Date.now().toString(36).slice(-6)}`;
}

async function onQuickPortraitSelected(event: Event) {
  const input = event.target as HTMLInputElement | null;
  const file = input?.files?.[0];
  if (input) input.value = "";
  if (uploadingAsset.value) return;
  if (!file || !canMutateRemote.value) return;
  if (!COLABONE_LIVE_PORTRAIT_EXT_RE.test(file.name)) {
    reportError(tb("人像仅支持 PNG、JPEG 或 WebP 格式"));
    return;
  }
  if (
    !validateLiveFileSize(
      file,
      COLABONE_LIVE_MAX_PORTRAIT_BYTES,
      t("live.fileKindPortrait"),
    )
  ) {
    return;
  }
  const current = activeAvatar.value;
  const targetAvatarId =
    current?.id || makeUniqueAvatarId(t("live.avatarDefaultName"));
  const targetName = current?.name || t("live.avatarDefaultName");
  const targetPersona = current?.persona_prompt || "";
  const targetTtsEngine = ttsEngine.value;
  const targetVoicePreset = current?.default_voice_preset || "shen_zhiyi";
  const targetActions = current?.actions
    ? serializeAvatarActions(current.actions)
    : [];
  const targetProducts = serializeAvatarProducts(displayProducts.value);

  uploadingAsset.value = true;
  try {
    const { dataUrl, base64 } = await readFileAsBase64(file);
    if (isUnmounted || !canMutateRemote.value) return;
    const res = await colabOneCommand<{
      id?: string;
      avatar?: ColabOneLiveAvatarManifest;
    }>("comfyui.live.avatars.save", {
      id: targetAvatarId,
      name: targetName,
      persona_prompt: targetPersona,
      portrait_b64: base64,
      portrait_filename: file.name,
      default_tts_engine: targetTtsEngine,
      default_voice_preset: targetVoicePreset,
      actions: targetActions,
      products: targetProducts,
    });
    if (isUnmounted) return;
    const savedId = res?.avatar?.id || res?.id || targetAvatarId;
    localPortraitPreviews.value = {
      ...localPortraitPreviews.value,
      [savedId]: dataUrl,
    };
    selectedAvatarId.value = savedId;
    notify(t("live.noticePortraitUploaded"), "success");
    emit("sync");
  } catch (err) {
    if (!isUnmounted) {
      reportError(err);
    }
  } finally {
    uploadingAsset.value = false;
  }
}

async function onClipFileSelected(
  event: Event,
  kind: "action_clip" | "driving_video",
) {
  const input = event.target as HTMLInputElement | null;
  const file = input?.files?.[0];
  if (input) input.value = "";
  if (uploadingAsset.value) return;
  if (!file || !canMutateRemote.value) return;

  const kindLabel =
    kind === "driving_video"
      ? t("live.fileKindDriving")
      : t("live.fileKindClip");
  if (!COLABONE_LIVE_CLIP_EXT_RE.test(file.name)) {
    reportError(
      t("live.errInvalidClipFormat", {
        kind: kindLabel,
        formats: "mp4, webm, mov, mkv",
      }),
    );
    return;
  }

  if (!validateLiveFileSize(file, COLABONE_LIVE_MAX_CLIP_BYTES, kindLabel)) {
    return;
  }

  const targetAvatarId = activeAvatar.value?.id;
  if (kind === "action_clip" && !targetAvatarId) {
    reportError(t("live.uploadClipNeedAvatarTip"));
    return;
  }

  uploadingAsset.value = true;
  try {
    const { base64 } = await readFileAsBase64(file);
    if (isUnmounted || !canMutateRemote.value) return;
    const stem = file.name.replace(/\.[^.]+$/, "").slice(0, 40) || "clip";
    const safeId = makeSafeLiveId(
      stem,
      kind === "driving_video" ? "drive" : "dance",
    );
    if (kind === "driving_video") {
      await colabOneCommand("comfyui.live.driving.upload", {
        id: safeId,
        label: stem,
        filename: file.name,
        data: base64,
      });
    } else {
      await colabOneCommand("comfyui.live.avatars.clip.upload", {
        avatar_id: targetAvatarId!,
        action_id: safeId,
        label: stem,
        kind: "dance",
        filename: file.name,
        data: base64,
      });
    }
    if (isUnmounted) return;
    notify(t("live.noticeClipUploaded"), "success");
    emit("sync");
  } catch (err) {
    if (!isUnmounted) {
      reportError(err);
    }
  } finally {
    uploadingAsset.value = false;
  }
}

// 1-Click Master Pack Builder Modal
const buildPackModalOpen = ref(false);
const buildPackSubmitting = ref(false);
const buildPackCreateNew = ref(false);
const buildPackName = ref("");
const buildPackAspect = ref<"9:16" | "16:9">("9:16");
const buildPackPortraitName = ref("");
const buildPackPortraitBase64 = ref("");
const buildPackPortraitPreview = ref("");

function openBuildPackModal() {
  const hasExisting = Boolean(activeAvatar.value);
  buildPackCreateNew.value = !hasExisting;
  buildPackName.value = hasExisting
    ? activeAvatar.value!.name
    : t("live.avatarDefaultName");
  buildPackAspect.value = stageAspect.value;
  buildPackPortraitName.value = "";
  buildPackPortraitBase64.value = "";
  buildPackPortraitPreview.value = hasExisting
    ? activePortraitPreview.value
    : "";
  buildPackModalOpen.value = true;
}

function onBuildPackCreateNewChange(checked: boolean | string | number) {
  const createNew = Boolean(checked);
  buildPackCreateNew.value = createNew;
  if (createNew) {
    buildPackName.value = "";
    if (!buildPackPortraitBase64.value) {
      buildPackPortraitPreview.value = "";
    }
  } else if (activeAvatar.value) {
    buildPackName.value = activeAvatar.value.name;
    if (!buildPackPortraitBase64.value) {
      buildPackPortraitPreview.value = activePortraitPreview.value;
    }
  }
}

async function onBuildPackPortraitPicked(event: Event) {
  const input = event.target as HTMLInputElement | null;
  const file = input?.files?.[0];
  if (input) input.value = "";
  if (!file) return;
  if (!COLABONE_LIVE_PORTRAIT_EXT_RE.test(file.name)) {
    reportError(tb("人像仅支持 PNG、JPEG 或 WebP 格式"));
    return;
  }
  if (
    !validateLiveFileSize(
      file,
      COLABONE_LIVE_MAX_PORTRAIT_BYTES,
      t("live.fileKindPortrait"),
    )
  ) {
    return;
  }
  try {
    const { dataUrl, base64 } = await readFileAsBase64(file);
    if (isUnmounted) return;
    buildPackPortraitName.value = file.name;
    buildPackPortraitBase64.value = base64;
    buildPackPortraitPreview.value = dataUrl;
  } catch (err) {
    if (!isUnmounted) {
      reportError(err);
    }
  }
}

async function submitBuildPack() {
  if (!canMutateRemote.value) return;
  const creatingNew = buildPackCreateNew.value || !activeAvatar.value;
  if (
    !buildPackPortraitBase64.value &&
    (creatingNew || !activeAvatar.value?.portrait_path)
  ) {
    reportError(t("live.errNeedPortrait"));
    return;
  }
  buildPackSubmitting.value = true;
  try {
    const displayName =
      buildPackName.value.trim() || t("live.avatarDefaultName");
    const targetAvatarId = creatingNew
      ? makeUniqueAvatarId(displayName)
      : activeAvatar.value!.id;
    const payload: Record<string, unknown> = {
      avatar_id: targetAvatarId,
      name: displayName,
      resolution: liveResolutionForAspect(buildPackAspect.value),
      fps: 25,
      overwrite: true,
    };
    if (buildPackPortraitBase64.value) {
      payload.portrait_b64 = buildPackPortraitBase64.value;
      payload.portrait_filename = buildPackPortraitName.value || "portrait.png";
    }
    await colabOneCommand("comfyui.live.avatars.build_pack", payload);
    if (buildPackPortraitPreview.value) {
      localPortraitPreviews.value = {
        ...localPortraitPreviews.value,
        [targetAvatarId]: buildPackPortraitPreview.value,
      };
    }
    pendingBuildAvatarId.value = targetAvatarId;
    selectedAvatarId.value = targetAvatarId;
    buildPackModalOpen.value = false;
    notify(t("live.noticePackBuilding"), "info");
    emit("sync");
  } catch (err) {
    reportError(err);
  } finally {
    buildPackSubmitting.value = false;
  }
}

// Product Knowledge Base CRUD & Live Card Push
const productModalOpen = ref(false);
const productSaving = ref(false);
const editingProductId = ref("");
const productFormName = ref("");
const productFormPrice = ref("");
const productFormTag = ref("");
const productFormHighlights = ref("");
const productFormScriptHint = ref("");

function openAddProductModal() {
  editingProductId.value = "";
  productFormName.value = "";
  productFormPrice.value = "";
  productFormTag.value = "";
  productFormHighlights.value = "";
  productFormScriptHint.value = "";
  productModalOpen.value = true;
}

function openEditProductModal(item: ColabOneLiveProductItem) {
  editingProductId.value = item.id;
  productFormName.value = item.name;
  productFormPrice.value = item.price;
  productFormTag.value = item.tag || "";
  productFormHighlights.value = productHighlightsList(item.highlights).join("\n");
  productFormScriptHint.value = item.script_hint || "";
  productModalOpen.value = true;
}

function parseHighlights(raw: string): string[] {
  return raw
    .split(/[\n,，]+/)
    .map((s) => s.trim())
    .filter(Boolean)
    .slice(0, 8);
}

async function persistAvatarProducts(nextProducts: ColabOneLiveProductItem[]) {
  const current = activeAvatar.value;
  const targetId = current?.id || selectedAvatarId.value || "default_anchor";
  if (canMutateRemote.value) {
    await colabOneCommand("comfyui.live.avatars.save", {
      id: targetId,
      name: current?.name || t("live.avatarDefaultName"),
      persona_prompt: current?.persona_prompt || "",
      default_tts_engine: current?.default_tts_engine || ttsEngine.value,
      default_voice_preset: current?.default_voice_preset || "shen_zhiyi",
      actions: current?.actions ? serializeAvatarActions(current.actions) : [],
      products: serializeAvatarProducts(nextProducts),
    });
    selectedAvatarId.value = targetId;
    localDraftProducts.value = [];
  } else {
    localDraftProducts.value = nextProducts;
  }
}

async function submitProductModal() {
  const name = productFormName.value.trim();
  if (!name) {
    reportError(t("live.errNeedProductName"));
    return;
  }
  const product: ColabOneLiveProductItem = {
    id: editingProductId.value || `prod_${Date.now()}`,
    name,
    price: productFormPrice.value.trim(),
    tag: productFormTag.value.trim() || undefined,
    highlights: parseHighlights(productFormHighlights.value).join(" · "),
    script_hint: productFormScriptHint.value.trim() || undefined,
    updated_at: Math.floor(Date.now() / 1000),
  };

  const nextProducts = displayProducts.value.slice();
  const idx = nextProducts.findIndex((p) => p.id === product.id);
  if (idx >= 0) {
    nextProducts[idx] = product;
  } else {
    nextProducts.push(product);
  }

  productSaving.value = true;
  try {
    await persistAvatarProducts(nextProducts);
    productModalOpen.value = false;
    notify(t("live.noticeProductSaved"), "success");
    emit("sync");
  } catch (err) {
    reportError(err);
  } finally {
    productSaving.value = false;
  }
}

async function deleteProduct(item: ColabOneLiveProductItem) {
  const nextProducts = displayProducts.value.filter((p) => p.id !== item.id);
  try {
    await persistAvatarProducts(nextProducts);
    if (activeCard.value?.id === item.id) {
      activeCard.value = null;
      sessionClient?.sendSetCard(null);
    }
    notify(t("live.noticeProductDeleted"), "success");
    emit("sync");
  } catch (err) {
    reportError(err);
  }
}

function pushProductCard(item: ColabOneLiveProductItem, alsoPitch: boolean) {
  if (alsoPitch && !sessionClient?.isConnected()) return;
  activeCard.value = item;
  if (sessionClient?.isConnected()) {
    sessionClient.sendSetCard(item.id, item);
    if (alsoPitch) {
      const cue = item.script_hint
        ? `[CARD:${item.id}] ${item.name} (${item.price}) · ${item.script_hint}`
        : `[CARD:${item.id}] ${item.name} (${item.price})`;
      sessionClient.sendTextInput(cue, "director");
    }
  }
  notify(t("live.noticeCardPushed", { name: item.name }), "info");
}

function dismissProductOverlay() {
  activeCard.value = null;
  if (sessionClient?.isConnected()) {
    sessionClient.sendSetCard(null);
  }
}

// LLM Brain save, clear & test
function onLlmModeChange(val: string | number) {
  const next: ColabOneLiveLlmMode =
    val === "external_api" ? "external_api" : "local_gguf";
  llmMode.value = next;
  llmModeInitialized = true;
  writeStoredLlmMode(next);
}

async function persistExternalLlmSettings(showToast = true): Promise<boolean> {
  if (!llmBaseUrl.value.trim() || !llmModel.value.trim()) {
    reportError(t("live.errNeedLlmFields"));
    return false;
  }
  llmSaving.value = true;
  try {
    const payload: Record<string, unknown> = {
      base_url: llmBaseUrl.value.trim(),
      model: llmModel.value.trim(),
    };
    if (llmApiKey.value.trim()) {
      payload.api_key = llmApiKey.value.trim();
    }
    await colabOneCommand("comfyui.live.llm.save", payload);
    if (llmApiKey.value.trim()) {
      llmHasSavedKey.value = true;
      llmApiKey.value = "";
    }
    llmDirty.value = false;
    if (showToast) {
      notify(t("live.noticeLlmSaved"), "success");
    }
    emit("sync");
    return true;
  } catch (err) {
    reportError(err);
    return false;
  } finally {
    llmSaving.value = false;
  }
}

async function saveLlmSettings() {
  writeStoredLlmMode(llmMode.value);
  if (llmMode.value === "local_gguf") {
    llmDirty.value = false;
    notify(t("live.noticeLlmSaved"), "success");
    return;
  }
  await persistExternalLlmSettings(true);
}

async function clearExternalLlmSettings() {
  llmClearing.value = true;
  try {
    await colabOneCommand("comfyui.live.llm.clear");
    llmBaseUrl.value = "";
    llmModel.value = "";
    llmApiKey.value = "";
    llmHasSavedKey.value = false;
    llmTestStatus.value = "idle";
    llmTestError.value = "";
    llmDirty.value = false;
    notify(t("live.noticeLlmCleared"), "success");
    emit("sync");
  } catch (err) {
    reportError(err);
  } finally {
    llmClearing.value = false;
  }
}

async function testExternalLlm() {
  if (!canMutateRemote.value) return;
  if (!llmBaseUrl.value.trim() || !llmModel.value.trim()) {
    reportError(t("live.errNeedLlmFields"));
    return;
  }
  llmTesting.value = true;
  llmTestStatus.value = "idle";
  llmTestError.value = "";
  try {
    const res = await colabOneCommand<{ ok?: boolean; error?: string }>(
      "comfyui.live.llm.test",
      {
        base_url: llmBaseUrl.value.trim(),
        model: llmModel.value.trim(),
        api_key: llmApiKey.value.trim() || undefined,
      },
    );
    if (res?.ok === false) {
      llmTestStatus.value = "failed";
      llmTestError.value = res.error || "";
      reportError(res.error || t("live.llmTestFailed"));
    } else {
      llmTestStatus.value = "ok";
      notify(t("live.noticeLlmTestPassed"), "success");
    }
  } catch (err) {
    llmTestStatus.value = "failed";
    llmTestError.value = String(err);
    reportError(err);
  } finally {
    llmTesting.value = false;
  }
}

function roleLabel(role: ColabOneLiveTranscriptEvent["role"]): string {
  if (role === "user") return t("live.roleUser");
  if (role === "director") return t("live.roleDirector");
  if (role === "system") return t("live.roleSystem");
  return t("live.roleAssistant");
}

onMounted(() => {
  syncIdleCanvasSize();
  if (typeof window !== "undefined") {
    window.addEventListener("keydown", onGlobalKeyDown);
    window.addEventListener("keyup", onGlobalKeyUp);
    window.addEventListener("blur", releasePttIfActive);
  }
  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", onVisibilityChange);
  }
});

onUnmounted(() => {
  isUnmounted = true;
  micOpGen += 1;
  if (typeof window !== "undefined") {
    window.removeEventListener("keydown", onGlobalKeyDown);
    window.removeEventListener("keyup", onGlobalKeyUp);
    window.removeEventListener("blur", releasePttIfActive);
  }
  if (typeof document !== "undefined") {
    document.removeEventListener("visibilitychange", onVisibilityChange);
  }
  teardownMediaSession();
});
</script>

<template>
  <section class="co-live-root" :aria-label="t('live.sectionAria')">
    <!-- Top Status & Engine Control Strip -->
    <div class="co-live-topbar">
      <div class="co-live-topbar-row">
        <div class="co-live-status-cluster">
          <ATag :color="directorStateMeta.color" class="co-live-state-pill">
            <ABadge :status="directorStateMeta.badge" />
            <span>{{ directorStateMeta.label }}</span>
          </ATag>
          <ATag :color="connBadgeMeta.color" class="co-live-conn-pill">
            {{ connBadgeMeta.label }}
          </ATag>
          <ColabOneTooltip :title="t('live.researchTooltip')">
            <ATag class="co-live-research-tag">
              <PhInfo :size="13" />
              <span>{{ t("live.researchBadge") }}</span>
            </ATag>
          </ColabOneTooltip>
          <ColabOneTooltip :title="vramBudgetView.tooltip">
            <ATag :color="vramBudgetView.color" class="co-live-vram-tag co-mono">
              <PhLightning :size="13" />
              <span>{{ vramBudgetView.label }}</span>
            </ATag>
          </ColabOneTooltip>
        </div>

        <ASpace :size="8" wrap class="co-live-primary-actions">
          <AButton
            v-if="liveRunning && !isConnected"
            size="middle"
            @click="connectLiveSession()"
          >
            <template #icon><PhArrowsClockwise :size="15" /></template>
            {{ t("live.reconnectBtn") }}
          </AButton>
          <AButton
            v-if="!liveRunning"
            type="primary"
            :loading="startingOrStopping || liveStarting"
            :disabled="!gpuReady || !hasLiveCapability || !canStartLiveTunnel"
            data-testid="live-studio-start-btn"
            @click="startLiveStudio"
          >
            <template #icon><PhBroadcast :size="16" /></template>
            {{ t("live.startBtn") }}
          </AButton>
          <AButton
            v-else
            type="primary"
            danger
            :loading="startingOrStopping"
            data-testid="live-studio-stop-btn"
            @click="stopLiveStudio"
          >
            <template #icon><PhStop :size="15" weight="fill" /></template>
            {{ t("live.stopBtn") }}
          </AButton>
        </ASpace>
      </div>

      <div class="co-live-engine-strip">
        <div class="co-live-engine-field">
          <span class="co-live-field-label">{{ t("live.engineVisualLabel") }}</span>
          <ASelect
            :value="visualEngine"
            size="small"
            class="co-live-engine-select"
            @change="onVisualEngineChange"
          >
            <ASelectOption value="flashhead_lite">
              {{ t("live.engineFlashHead") }}
            </ASelectOption>
            <ASelectOption value="musetalk_bank">
              {{ t("live.engineMuseTalk") }}
            </ASelectOption>
          </ASelect>
        </div>

        <div class="co-live-engine-field">
          <span class="co-live-field-label">{{ t("live.engineTtsLabel") }}</span>
          <ASelect
            :value="ttsEngine"
            size="small"
            class="co-live-tts-select"
            @change="onTtsEngineChange"
          >
            <ASelectOption value="qwen3_tts">
              {{ t("live.engineQwen3Tts") }}
            </ASelectOption>
            <ASelectOption value="voxcpm2">
              {{ t("live.engineVoxCpm2") }}
            </ASelectOption>
          </ASelect>
        </div>

        <ColabOneTooltip :title="t('live.personaLiveTooltip')">
          <label class="co-live-personalive-toggle">
            <ASwitch
              size="small"
              :checked="personaliveEnabled"
              @change="onPersonaLiveChange"
            />
            <span class="co-live-personalive-text">
              {{ t("live.personaLiveLabel") }}
            </span>
            <ATag color="warning" class="co-live-exp-chip">
              {{ t("live.personaLiveExperimental") }}
            </ATag>
          </label>
        </ColabOneTooltip>
      </div>
    </div>

    <!-- Capability / Tunnel / Offline Inline Guidance -->
    <AAlert
      v-if="gpuReady && !hasLiveCapability"
      type="warning"
      show-icon
      class="co-live-prereq-alert"
    >
      <template #icon><PhWarningCircle :size="18" /></template>
      <template #message>{{ t("live.upgradeRequiredTitle") }}</template>
      <template #description>
        <div class="co-live-alert-body">
          <span>{{ t("live.upgradeRequiredDesc") }}</span>
          <AButton type="primary" size="small" @click="emit('upgrade')">
            <template #icon><PhArrowsClockwise :size="14" /></template>
            {{ t("live.upgradeNowBtn") }}
          </AButton>
        </div>
      </template>
    </AAlert>

    <AAlert
      v-else-if="gpuReady && hasLiveCapability && !tunnelReady"
      type="info"
      show-icon
      class="co-live-prereq-alert"
    >
      <template #icon><PhPlug :size="18" /></template>
      <template #message>{{ t("live.tunnelRequiredTitle") }}</template>
      <template #description>
        <div class="co-live-alert-body">
          <span>
            {{
              hasSavedCanvasConfig
                ? t("live.tunnelAutoStartDesc")
                : t("live.tunnelRequiredDesc")
            }}
          </span>
          <AButton
            type="primary"
            ghost
            size="small"
            @click="emit('open-canvas-tab')"
          >
            {{ t("live.goCanvasTunnelBtn") }}
          </AButton>
        </div>
      </template>
    </AAlert>

    <AAlert
      v-else-if="!gpuReady"
      type="info"
      show-icon
      class="co-live-prereq-alert"
    >
      <template #icon><PhInfo :size="18" /></template>
      <template #message>{{ t("live.offlineHintTitle") }}</template>
      <template #description>{{ t("live.offlineHintDesc") }}</template>
    </AAlert>

    <AAlert
      v-if="liveStatus?.error"
      type="error"
      show-icon
      class="co-live-prereq-alert"
    >
      <template #message>
        <ColabOnePrivacyText
          :text="tb(liveStatus.error)"
          :context="privacyContext"
        />
      </template>
    </AAlert>

    <!-- Responsive 2-Column Studio Workspace -->
    <div class="co-live-workspace">
      <!-- LEFT MAIN STAGE COLUMN -->
      <div class="co-live-stage-col">
        <div
          class="co-live-stage"
          :class="{
            'is-portrait': stageAspect === '9:16',
            'is-landscape': stageAspect === '16:9',
            'is-live': isConnected,
          }"
          @click="resumeSuspendedAudio"
        >
          <canvas
            ref="stageCanvasRef"
            class="co-live-canvas"
            role="img"
            :aria-label="t('live.stageCanvasAria')"
          />
          <video
            ref="mseVideoRef"
            class="co-live-mse-video"
            playsinline
            muted
            aria-hidden="true"
          />

          <!-- Idle / Offline Stage Placeholder -->
          <div v-if="!hasVideoFrame" class="co-live-stage-placeholder">
            <img
              v-if="activePortraitPreview"
              :src="activePortraitPreview"
              alt=""
              class="co-live-placeholder-avatar"
            />
            <div v-else class="co-live-placeholder-emblem">
              <PhVideoCamera :size="36" weight="duotone" />
            </div>
            <h3>{{ t("live.stageIdleTitle") }}</h3>
            <p>
              {{
                connState === "connecting" ||
                connState === "authenticating" ||
                connState === "reconnecting"
                  ? t("live.stageConnectingDesc")
                  : t("live.stageIdleDesc")
              }}
            </p>
          </div>

          <!-- Top Flex Overlay Bar: Telemetry HUD & Stage Controls (wraps cleanly, zero overlap) -->
          <div class="co-live-stage-top-bar">
            <div class="co-live-hud co-mono" aria-live="off">
              <span class="co-live-hud-item">
                <span class="co-live-hud-k">{{ t("live.hudFps") }}</span>
                <span class="co-live-hud-v">
                  {{ liveTelemetry.fps > 0 ? liveTelemetry.fps.toFixed(1) : "--" }}
                </span>
              </span>
              <span class="co-live-hud-sep" aria-hidden="true" />
              <span class="co-live-hud-item">
                <span class="co-live-hud-k">{{ t("live.hudTtft") }}</span>
                <span class="co-live-hud-v">{{ formatMs(liveTelemetry.ttft_ms) }}</span>
              </span>
              <span class="co-live-hud-sep" aria-hidden="true" />
              <span class="co-live-hud-item">
                <span class="co-live-hud-k">{{ t("live.hudTtfa") }}</span>
                <span class="co-live-hud-v">{{ formatMs(liveTelemetry.ttfa_ms) }}</span>
              </span>
              <span class="co-live-hud-sep" aria-hidden="true" />
              <span class="co-live-hud-item">
                <span class="co-live-hud-k">{{ t("live.hudE2e") }}</span>
                <span class="co-live-hud-v">{{ formatMs(liveTelemetry.e2e_ms) }}</span>
              </span>
              <span class="co-live-hud-sep" aria-hidden="true" />
              <span class="co-live-hud-item">
                <span class="co-live-hud-k">{{ t("live.hudRtt") }}</span>
                <span class="co-live-hud-v">{{ formatMs(liveTelemetry.rtt_ms) }}</span>
              </span>
              <span class="co-live-hud-sep" aria-hidden="true" />
              <span class="co-live-hud-item">
                <span class="co-live-hud-k">{{ t("live.hudVram") }}</span>
                <span class="co-live-hud-v">{{ hudVramText }}</span>
              </span>
            </div>

            <div class="co-live-stage-tools">
              <button
                v-if="audioSuspended && isConnected"
                type="button"
                class="co-live-stage-chip is-resume-audio"
                @click.stop="resumeSuspendedAudio"
              >
                <PhSpeakerHigh :size="13" />
                <span>{{ t("live.resumeAudioBtn") }}</span>
              </button>
              <button
                type="button"
                class="co-live-stage-chip"
                :disabled="liveRunning"
                @click.stop="
                  stageAspect = stageAspect === '9:16' ? '16:9' : '9:16'
                "
              >
                {{
                  stageAspect === "9:16"
                    ? t("live.aspectPortrait")
                    : t("live.aspectLandscape")
                }}
              </button>
              <div class="co-live-volume-pill" @click.stop>
                <ColabOneTooltip
                  :title="audioMuted ? t('live.unmuteAudio') : t('live.muteAudio')"
                >
                  <button
                    type="button"
                    class="co-live-icon-btn"
                    :aria-label="
                      audioMuted ? t('live.unmuteAudio') : t('live.muteAudio')
                    "
                    @click="toggleAudioMute"
                  >
                    <PhSpeakerSlash v-if="audioMuted" :size="15" />
                    <PhSpeakerHigh v-else :size="15" />
                  </button>
                </ColabOneTooltip>
                <ASlider
                  :value="audioMuted ? 0 : audioVolume"
                  :min="0"
                  :max="100"
                  :tooltip-open="false"
                  class="co-live-volume-slider"
                  :aria-label="t('live.volumeLabel')"
                  @change="onVolumeChange"
                />
              </div>
            </div>
          </div>

          <!-- Floating Product Overlay Card -->
          <transition name="co-live-card-pop">
            <div
              v-if="activeCard"
              class="co-live-product-overlay"
              role="region"
              :aria-label="activeCard.name"
              @click.stop
            >
              <div class="co-live-overlay-head">
                <span class="co-live-overlay-tag">
                  <PhTag :size="12" weight="fill" />
                  <span>{{ activeCard.tag || t("live.productOverlayTag") }}</span>
                </span>
                <button
                  type="button"
                  class="co-live-overlay-close"
                  :aria-label="t('live.productOverlayDismiss')"
                  @click="dismissProductOverlay"
                >
                  <PhX :size="14" />
                </button>
              </div>
              <div class="co-live-overlay-title">{{ activeCard.name }}</div>
              <div v-if="activeCard.price" class="co-live-overlay-price co-mono">
                {{ activeCard.price }}
              </div>
              <ul
                v-if="productHighlightsList(activeCard.highlights).length"
                class="co-live-overlay-highlights"
              >
                <li
                  v-for="(hl, idx) in productHighlightsList(
                    activeCard.highlights,
                  ).slice(0, 4)"
                  :key="idx"
                >
                  {{ hl }}
                </li>
              </ul>
            </div>
          </transition>
        </div>

        <!-- Live Transcript Strip -->
        <div class="co-live-transcript-strip">
          <div class="co-live-transcript-head">
            <PhWaveform :size="15" />
            <span>{{ t("live.transcriptTitle") }}</span>
          </div>
          <div v-if="!visibleTranscripts.length" class="co-live-transcript-empty">
            {{ t("live.transcriptEmpty") }}
          </div>
          <div v-else class="co-live-transcript-list">
            <div
              v-for="item in visibleTranscripts"
              :key="item.id"
              class="co-live-transcript-row"
              :class="`is-${item.role}`"
            >
              <span class="co-live-transcript-role">{{ roleLabel(item.role) }}</span>
              <span class="co-live-transcript-text">{{ item.text }}</span>
            </div>
          </div>
        </div>

        <!-- Bottom Voice & Director Control Bar -->
        <div class="co-live-controls">
          <div class="co-live-controls-top">
            <div class="co-live-mic-group">
              <AButton
                :type="micActive ? 'primary' : 'default'"
                :danger="micActive && pttPressed"
                :loading="micStarting"
                :disabled="micStarting"
                @click="toggleMic"
              >
                <template #icon>
                  <PhMicrophone v-if="micActive" :size="16" weight="fill" />
                  <PhMicrophoneSlash v-else :size="16" />
                </template>
                {{ micActive ? t("live.micStop") : t("live.micStart") }}
              </AButton>

              <div
                class="co-live-rms-meter"
                role="meter"
                :aria-label="t('live.micLevelAria')"
                :aria-valuenow="Math.round(micLevel * 100)"
                aria-valuemin="0"
                aria-valuemax="100"
              >
                <div
                  class="co-live-rms-fill"
                  :class="{
                    'is-active': micActive && (micMode === 'vad' || pttPressed),
                  }"
                  :style="{ width: `${Math.round(micLevel * 100)}%` }"
                />
              </div>

              <AButton
                v-if="micMode === 'ptt' && micActive"
                size="small"
                :type="pttPressed ? 'primary' : 'default'"
                @mousedown="setPttHolding(true)"
                @mouseup="setPttHolding(false)"
                @mouseleave="setPttHolding(false)"
                @touchstart.prevent="setPttHolding(true)"
                @touchend.prevent="setPttHolding(false)"
                @touchcancel.prevent="setPttHolding(false)"
              >
                {{
                  pttPressed ? t("live.pttSpeakingBtn") : t("live.pttHoldBtn")
                }}
              </AButton>
            </div>

            <div class="co-live-mode-switches">
              <ASegmented
                :value="micMode"
                :options="micModeOptions"
                size="small"
                @change="onMicModeChange"
              />
              <ColabOneTooltip :title="t('live.directorModeTooltip')">
                <span>
                  <ASegmented
                    :value="directorMode"
                    :options="directorModeOptions"
                    size="small"
                    @change="onDirectorModeChange"
                  />
                </span>
              </ColabOneTooltip>
              <ColabOneTooltip :title="t('live.bargeInTooltip')">
                <AButton
                  type="primary"
                  danger
                  :disabled="!canBargeIn"
                  data-testid="live-barge-in-btn"
                  @click="triggerBargeIn"
                >
                  <template #icon><PhHandPalm :size="15" weight="fill" /></template>
                  {{ t("live.bargeInBtn") }}
                </AButton>
              </ColabOneTooltip>
            </div>
          </div>

          <form
            class="co-live-text-form"
            @submit.prevent="sendDirectiveText"
          >
            <AInput
              v-model:value="textInput"
              :placeholder="
                directorMode === 'director'
                  ? t('live.inputPlaceholderDirector')
                  : t('live.inputPlaceholderDialogue')
              "
              :disabled="!isConnected"
              allow-clear
            />
            <AButton
              type="primary"
              html-type="submit"
              :disabled="!isConnected || !textInput.trim()"
            >
              <template #icon><PhPaperPlaneTilt :size="15" weight="fill" /></template>
              {{ t("live.sendBtn") }}
            </AButton>
          </form>
        </div>
      </div>

      <!-- RIGHT DIRECTOR SIDEBAR -->
      <aside class="co-live-inspector">
        <!-- 1. Avatar Master Pack -->
        <div class="co-live-side-card">
          <div class="co-live-side-head">
            <div class="co-live-side-title">
              <PhUserCircle :size="17" />
              <h3>{{ t("live.avatarCardTitle") }}</h3>
            </div>
            <ATag v-if="activeBuildPackTask" color="processing">
              {{
                activeBuildPackProgressPct > 0
                  ? `${t("live.avatarPackBuilding")} ${activeBuildPackProgressPct}%`
                  : t("live.avatarPackBuilding")
              }}
            </ATag>
            <ATag
              v-else
              :color="isAvatarPackReady(activeAvatar) ? 'success' : 'default'"
            >
              {{
                isAvatarPackReady(activeAvatar)
                  ? t("live.avatarPackReady")
                  : t("live.avatarPackSingle")
              }}
            </ATag>
          </div>

          <div class="co-live-avatar-select-row">
            <ASelect
              :value="selectedAvatarId || undefined"
              :placeholder="t('live.avatarSelectPlaceholder')"
              class="co-live-full-select"
              @change="onAvatarSelect"
            >
              <ASelectOption
                v-for="av in remoteAvatars"
                :key="av.id"
                :value="av.id"
              >
                {{ av.name }}
              </ASelectOption>
            </ASelect>
            <APopconfirm
              v-if="activeAvatar"
              :title="t('live.avatarDeleteConfirm', { name: activeAvatar.name })"
              :disabled="!canMutateRemote || uploadingAsset"
              @confirm="deleteSelectedAvatar"
            >
              <ColabOneTooltip :title="t('live.avatarDeleteBtn')">
                <AButton
                  size="small"
                  type="text"
                  danger
                  :disabled="!canMutateRemote || uploadingAsset"
                  :aria-label="t('live.avatarDeleteBtn')"
                >
                  <template #icon><PhTrash :size="15" /></template>
                </AButton>
              </ColabOneTooltip>
            </APopconfirm>
          </div>

          <div class="co-live-avatar-actions">
            <AButton
              size="small"
              :loading="uploadingAsset"
              :disabled="!canMutateRemote"
              @click="portraitInputRef?.click()"
            >
              <template #icon><PhUploadSimple :size="14" /></template>
              {{ t("live.uploadPortraitBtn") }}
            </AButton>
            <AButton
              type="primary"
              ghost
              size="small"
              :disabled="!canMutateRemote"
              @click="openBuildPackModal"
            >
              <template #icon><PhMagicWand :size="14" /></template>
              {{ t("live.buildPackBtn") }}
            </AButton>
            <ColabOneTooltip
              :title="!activeAvatar ? t('live.uploadClipNeedAvatarTip') : ''"
            >
              <span>
                <AButton
                  size="small"
                  :loading="uploadingAsset"
                  :disabled="!canMutateRemote || !activeAvatar"
                  @click="clipInputRef?.click()"
                >
                  <template #icon><PhFilmSlate :size="14" /></template>
                  {{ t("live.uploadClipBtn") }}
                </AButton>
              </span>
            </ColabOneTooltip>
            <AButton
              v-if="personaliveEnabled"
              size="small"
              :loading="uploadingAsset"
              :disabled="!canMutateRemote"
              @click="drivingVideoInputRef?.click()"
            >
              <template #icon><PhMusicNotes :size="14" /></template>
              {{ t("live.uploadDrivingBtn") }}
            </AButton>
          </div>

          <input
            ref="portraitInputRef"
            type="file"
            accept="image/png,image/jpeg,image/webp"
            class="co-sr-only"
            tabindex="-1"
            aria-hidden="true"
            @change="onQuickPortraitSelected"
          />
          <input
            ref="clipInputRef"
            type="file"
            accept="video/mp4,video/webm,video/quicktime,.mkv,video/x-matroska"
            class="co-sr-only"
            tabindex="-1"
            aria-hidden="true"
            @change="(e) => onClipFileSelected(e, 'action_clip')"
          />
          <input
            ref="drivingVideoInputRef"
            type="file"
            accept="video/mp4,video/webm,video/quicktime,.mkv,video/x-matroska"
            class="co-sr-only"
            tabindex="-1"
            aria-hidden="true"
            @change="(e) => onClipFileSelected(e, 'driving_video')"
          />
        </div>

        <!-- 2. Actions & Talent Quick Wall (+ PersonaLive Driving Motions) -->
        <div class="co-live-side-card">
          <div class="co-live-side-head">
            <div class="co-live-side-title">
              <PhSparkle :size="17" />
              <h3>{{ t("live.actionsCardTitle") }}</h3>
            </div>
          </div>

          <div class="co-live-action-wall">
            <button
              v-for="act in displayActions"
              :key="act.id"
              type="button"
              class="co-live-action-btn"
              :class="{ 'is-active': activeActionId === act.id }"
              :disabled="
                !isConnected ||
                (act.kind === 'personalive_drive' && !personaliveEnabled)
              "
              @click="triggerAction(act)"
            >
              <PhHeart
                v-if="act.id === 'action_heart' || act.id === 'heart'"
                :size="14"
                weight="duotone"
              />
              <PhMusicNotes
                v-else-if="act.kind === 'dance' || act.kind === 'personalive_drive'"
                :size="14"
                weight="duotone"
              />
              <PhPlay v-else :size="13" weight="fill" />
              <span>{{ act.label || act.name || act.id }}</span>
            </button>
          </div>

          <div
            v-if="drivingMotions.length > 0"
            class="co-live-driving-section"
          >
            <div class="co-live-driving-head">
              <PhMusicNotes :size="14" />
              <span>{{ t("live.drivingMotionsTitle") }}</span>
            </div>
            <div class="co-live-driving-list">
              <div
                v-for="motion in drivingMotions"
                :key="motion.id"
                class="co-live-driving-row"
              >
                <ColabOneTooltip
                  :title="
                    !personaliveEnabled
                      ? t('live.drivingRequiresPersonaLiveTip')
                      : ''
                  "
                >
                  <span class="co-live-driving-trigger-wrap">
                    <button
                      type="button"
                      class="co-live-action-btn co-live-driving-trigger"
                      :class="{ 'is-active': activeActionId === motion.id }"
                      :disabled="!isConnected || !personaliveEnabled"
                      @click="triggerDrivingMotion(motion)"
                    >
                      <PhMusicNotes :size="13" weight="duotone" />
                      <span>{{ motion.label || motion.id }}</span>
                    </button>
                  </span>
                </ColabOneTooltip>
                <APopconfirm
                  :title="
                    t('live.drivingDeleteConfirm', {
                      name: motion.label || motion.id,
                    })
                  "
                  :disabled="!canMutateRemote || uploadingAsset"
                  @confirm="deleteDrivingMotion(motion)"
                >
                  <ColabOneTooltip :title="t('live.drivingDeleteBtn')">
                    <AButton
                      size="small"
                      type="text"
                      danger
                      :disabled="!canMutateRemote || uploadingAsset"
                      :aria-label="t('live.drivingDeleteBtn')"
                    >
                      <template #icon><PhTrash :size="14" /></template>
                    </AButton>
                  </ColabOneTooltip>
                </APopconfirm>
              </div>
            </div>
          </div>
        </div>

        <!-- 3. Product Knowledge Base -->
        <div class="co-live-side-card">
          <div class="co-live-side-head">
            <div class="co-live-side-title">
              <PhShoppingBag :size="17" />
              <h3>{{ t("live.productsCardTitle") }}</h3>
            </div>
            <AButton size="small" @click="openAddProductModal">
              <template #icon><PhPlus :size="14" /></template>
              {{ t("live.productAddBtn") }}
            </AButton>
          </div>

          <div v-if="!displayProducts.length" class="co-live-empty-mini">
            {{ t("live.productEmpty") }}
          </div>

          <div v-else class="co-live-product-list">
            <div
              v-for="prod in displayProducts"
              :key="prod.id"
              class="co-live-product-item"
              :class="{ 'is-active': activeCard?.id === prod.id }"
            >
              <div class="co-live-product-main">
                <div class="co-live-product-title-row">
                  <span class="co-live-product-name">{{ prod.name }}</span>
                  <span v-if="prod.price" class="co-live-product-price co-mono">
                    {{ prod.price }}
                  </span>
                </div>
                <div
                  v-if="productHighlightsList(prod.highlights).length"
                  class="co-live-product-tags"
                >
                  <ATag
                    v-for="(hl, idx) in productHighlightsList(
                      prod.highlights,
                    ).slice(0, 3)"
                    :key="idx"
                    class="co-live-mini-tag"
                  >
                    {{ hl }}
                  </ATag>
                </div>
              </div>
              <div class="co-live-product-ops">
                <AButton
                  size="small"
                  type="primary"
                  ghost
                  :disabled="!isConnected"
                  @click="pushProductCard(prod, true)"
                >
                  <template #icon><PhMegaphone :size="13" /></template>
                  {{ t("live.productPitchBtn") }}
                </AButton>
                <AButton size="small" @click="pushProductCard(prod, false)">
                  {{ t("live.productPopCardBtn") }}
                </AButton>
                <ColabOneTooltip :title="t('live.productEditBtn')">
                  <AButton
                    size="small"
                    type="text"
                    :aria-label="t('live.productEditBtn')"
                    @click="openEditProductModal(prod)"
                  >
                    <template #icon><PhPencilSimple :size="14" /></template>
                  </AButton>
                </ColabOneTooltip>
                <APopconfirm
                  :title="t('live.productDeleteConfirm', { name: prod.name })"
                  @confirm="deleteProduct(prod)"
                >
                  <ColabOneTooltip :title="t('live.productDeleteBtn')">
                    <AButton
                      size="small"
                      type="text"
                      danger
                      :aria-label="t('live.productDeleteBtn')"
                    >
                      <template #icon><PhTrash :size="14" /></template>
                    </AButton>
                  </ColabOneTooltip>
                </APopconfirm>
              </div>
            </div>
          </div>
        </div>

        <!-- 4. LLM Brain Settings -->
        <div class="co-live-side-card">
          <div class="co-live-side-head">
            <div class="co-live-side-title">
              <PhBrain :size="17" />
              <h3>{{ t("live.llmCardTitle") }}</h3>
            </div>
            <ATag
              v-if="llmMode === 'external_api' && llmTestStatus === 'ok'"
              color="success"
            >
              <PhCheckCircle :size="13" weight="fill" />
              <span>{{ t("live.llmTestOk") }}</span>
            </ATag>
            <ATag
              v-else-if="llmMode === 'external_api' && llmTestStatus === 'failed'"
              color="error"
            >
              <PhWarningCircle :size="13" weight="fill" />
              <span>{{ t("live.llmTestFailed") }}</span>
            </ATag>
          </div>

          <ASegmented
            :value="llmMode"
            :options="llmModeOptions"
            block
            size="small"
            class="co-live-llm-segment"
            @change="onLlmModeChange"
          />

          <div v-if="llmMode === 'local_gguf'" class="co-live-llm-local-box">
            <p>{{ t("live.llmLocalHint") }}</p>
            <ATag :color="loadedLocalModelName ? 'success' : 'default'">
              {{
                loadedLocalModelName
                  ? t("live.llmLocalLoaded", { name: loadedLocalModelName })
                  : t("live.llmLocalNotLoaded")
              }}
            </ATag>
            <div class="co-live-llm-actions">
              <AButton
                size="small"
                type="primary"
                :loading="llmSaving"
                @click="saveLlmSettings"
              >
                {{ t("live.llmSaveBtn") }}
              </AButton>
            </div>
          </div>

          <div v-else class="co-live-llm-form">
            <div class="co-live-form-field">
              <span>{{ t("live.llmBaseUrlLabel") }}</span>
              <ColabOnePrivacyInput
                :value="llmBaseUrl"
                size="small"
                :allow-clear="false"
                :placeholder="t('live.llmBaseUrlPlaceholder')"
                @update:value="
                  (v) => {
                    llmBaseUrl = v;
                    llmDirty = true;
                  }
                "
              />
            </div>
            <label class="co-live-form-field">
              <span>{{ t("live.llmModelLabel") }}</span>
              <AInput
                v-model:value="llmModel"
                size="small"
                :placeholder="t('live.llmModelPlaceholder')"
                @input="llmDirty = true"
              />
            </label>
            <div class="co-live-form-field">
              <div class="co-live-llm-key-head">
                <span>{{ t("live.llmApiKeyLabel") }}</span>
                <ATag
                  v-if="llmHasSavedKey && maskedExternalApiKey && !colabOnePrivacyActive"
                  color="success"
                  class="co-live-mini-tag co-mono"
                >
                  {{ t("live.llmSavedKeyBadge", { key: maskedExternalApiKey }) }}
                </ATag>
              </div>
              <ColabOneSecretInput
                :value="llmApiKey"
                :has-saved="llmHasSavedKey"
                :placeholder="t('live.llmApiKeyPlaceholder')"
                :saved-placeholder="t('live.llmApiKeySavedPlaceholder')"
                @update:value="
                  (v) => {
                    llmApiKey = v;
                    llmDirty = true;
                  }
                "
              />
            </div>
            <div v-if="llmTestError" class="co-live-llm-err">
              <ColabOnePrivacyText
                :text="tb(llmTestError)"
                :context="privacyContext"
              />
            </div>
            <div class="co-live-llm-actions">
              <AButton
                size="small"
                type="primary"
                :loading="llmSaving"
                @click="saveLlmSettings"
              >
                {{ t("live.llmSaveBtn") }}
              </AButton>
              <AButton
                size="small"
                :loading="llmTesting"
                :disabled="!canMutateRemote"
                @click="testExternalLlm"
              >
                {{ t("live.llmTestBtn") }}
              </AButton>
              <APopconfirm
                v-if="canClearExternalLlm"
                :title="t('live.llmClearConfirm')"
                @confirm="clearExternalLlmSettings"
              >
                <AButton size="small" danger ghost :loading="llmClearing">
                  {{ t("live.llmClearBtn") }}
                </AButton>
              </APopconfirm>
            </div>
          </div>
        </div>
      </aside>
    </div>

    <!-- Modal: 1-Click Build Master Pack from Single Portrait -->
    <AModal
      v-model:open="buildPackModalOpen"
      :title="t('live.buildPackModalTitle')"
      :confirm-loading="buildPackSubmitting"
      :closable="!buildPackSubmitting"
      :mask-closable="!buildPackSubmitting"
      :keyboard="!buildPackSubmitting"
      :ok-text="t('live.buildPackConfirm')"
      :z-index="1050"
      @ok="submitBuildPack"
    >
      <div class="co-live-modal-body">
        <p class="co-live-modal-desc">{{ t("live.buildPackModalDesc") }}</p>
        <label
          v-if="activeAvatar"
          class="co-live-create-new-toggle"
        >
          <ASwitch
            size="small"
            :checked="buildPackCreateNew"
            @change="onBuildPackCreateNewChange"
          />
          <span>{{ t("live.buildPackCreateNewLabel") }}</span>
        </label>
        <label class="co-live-form-field">
          <span>{{ t("live.buildPackNameLabel") }}</span>
          <AInput
            v-model:value="buildPackName"
            :placeholder="t('live.buildPackNamePlaceholder')"
          />
        </label>
        <div class="co-live-form-field">
          <span>{{ t("live.buildPackAspectLabel") }}</span>
          <ASegmented
            :value="buildPackAspect"
            :options="[
              { label: t('live.aspectPortrait'), value: '9:16' },
              { label: t('live.aspectLandscape'), value: '16:9' },
            ]"
            size="small"
            @change="(v) => (buildPackAspect = v === '16:9' ? '16:9' : '9:16')"
          />
        </div>
        <div class="co-live-form-field">
          <span>{{ t("live.buildPackPortraitLabel") }}</span>
          <div class="co-live-pack-picker">
            <img
              v-if="buildPackPortraitPreview"
              :src="buildPackPortraitPreview"
              alt=""
              class="co-live-pack-thumb"
            />
            <AButton @click="packPortraitInputRef?.click()">
              <template #icon><PhUploadSimple :size="15" /></template>
              {{ buildPackPortraitName || t("live.buildPackPickPortrait") }}
            </AButton>
          </div>
          <input
            ref="packPortraitInputRef"
            type="file"
            accept="image/png,image/jpeg,image/webp"
            class="co-sr-only"
            tabindex="-1"
            aria-hidden="true"
            @change="onBuildPackPortraitPicked"
          />
        </div>
      </div>
    </AModal>

    <!-- Modal: Add / Edit Product Card -->
    <AModal
      v-model:open="productModalOpen"
      :title="
        editingProductId
          ? t('live.productModalEditTitle')
          : t('live.productModalAddTitle')
      "
      :confirm-loading="productSaving"
      :closable="!productSaving"
      :mask-closable="!productSaving"
      :keyboard="!productSaving"
      :z-index="1050"
      @ok="submitProductModal"
    >
      <div class="co-live-modal-body">
        <label class="co-live-form-field">
          <span>{{ t("live.productNameLabel") }}</span>
          <AInput
            v-model:value="productFormName"
            :placeholder="t('live.productNamePlaceholder')"
          />
        </label>
        <div class="co-live-form-grid-2">
          <label class="co-live-form-field">
            <span>{{ t("live.productPriceLabel") }}</span>
            <AInput
              v-model:value="productFormPrice"
              :placeholder="t('live.productPricePlaceholder')"
            />
          </label>
          <label class="co-live-form-field">
            <span>{{ t("live.productTagLabel") }}</span>
            <AInput
              v-model:value="productFormTag"
              :placeholder="t('live.productTagPlaceholder')"
            />
          </label>
        </div>
        <label class="co-live-form-field">
          <span>{{ t("live.productHighlightsLabel") }}</span>
          <ATextarea
            v-model:value="productFormHighlights"
            :rows="3"
            :placeholder="t('live.productHighlightsPlaceholder')"
          />
        </label>
        <label class="co-live-form-field">
          <span>{{ t("live.productScriptHintLabel") }}</span>
          <ATextarea
            v-model:value="productFormScriptHint"
            :rows="2"
            :placeholder="t('live.productScriptHintPlaceholder')"
          />
        </label>
      </div>
    </AModal>
  </section>
</template>

<style scoped>
.co-live-root {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.co-live-topbar {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 18px;
  border-radius: var(--co-radius-lg);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
}

.co-live-topbar-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.co-live-status-cluster {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-live-research-tag {
  cursor: help;
}

.co-live-engine-strip {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
  padding-top: 10px;
  border-top: 1px solid var(--co-border-secondary);
}

.co-live-engine-field {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.co-live-field-label {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-secondary-text);
  white-space: nowrap;
}

.co-live-engine-select {
  min-width: 230px;
}

.co-live-tts-select {
  min-width: 155px;
}

@media (max-width: 680px) {
  .co-live-engine-field {
    width: 100%;
    min-width: 0;
  }

  .co-live-engine-select,
  .co-live-tts-select {
    min-width: 0;
    flex: 1;
  }
}

.co-live-personalive-toggle {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  cursor: pointer;
  user-select: none;
}

.co-live-personalive-text {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-text);
}

.co-live-exp-chip {
  font-size: 11px;
  min-height: 19px;
  padding: 0 7px;
}

.co-live-prereq-alert {
  border-radius: var(--co-radius-md);
}

.co-live-alert-body {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.co-live-workspace {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(320px, 420px);
  gap: 16px;
  align-items: start;
}

@media (max-width: 1080px) {
  .co-live-workspace {
    grid-template-columns: minmax(0, 1fr);
  }
}

.co-live-stage-col {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}

.co-live-stage {
  position: relative;
  width: 100%;
  min-height: 460px;
  max-height: 680px;
  border-radius: var(--co-radius-lg);
  border: 1px solid var(--co-border-secondary);
  background: radial-gradient(
    circle at 50% 25%,
    rgba(59, 130, 246, 0.14),
    #0e1118 72%
  );
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: var(--co-card-shadow);
}

.co-live-stage.is-live {
  border-color: rgba(16, 185, 129, 0.42);
}

.co-live-canvas {
  display: block;
  max-width: 100%;
  max-height: 680px;
  width: auto;
  height: 100%;
  object-fit: contain;
}

.co-live-stage.is-landscape .co-live-canvas {
  width: 100%;
  height: auto;
  aspect-ratio: 16 / 9;
}

.co-live-stage.is-portrait .co-live-canvas {
  height: min(640px, 66vh);
  aspect-ratio: 9 / 16;
}

.co-live-mse-video {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
  pointer-events: none;
}

.co-live-stage-placeholder {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 24px;
  gap: 10px;
  color: #e5e7eb;
  pointer-events: none;
}

.co-live-placeholder-emblem {
  width: 72px;
  height: 72px;
  border-radius: 9999px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(59, 130, 246, 0.18);
  border: 1px solid rgba(147, 197, 253, 0.34);
  color: #93c5fd;
}

.co-live-placeholder-avatar {
  width: 132px;
  height: 132px;
  border-radius: 20px;
  object-fit: cover;
  border: 2px solid rgba(255, 255, 255, 0.24);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.45);
}

.co-live-stage-placeholder h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: #f8fafc;
}

.co-live-stage-placeholder p {
  margin: 0;
  max-width: 400px;
  font-size: 13px;
  line-height: 1.55;
  color: #cbd5e1;
}

.co-live-stage-top-bar {
  position: absolute;
  top: 12px;
  left: 12px;
  right: 12px;
  z-index: 4;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  pointer-events: none;
}

.co-live-hud {
  pointer-events: auto;
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding: 5px 11px;
  border-radius: var(--co-radius-pill);
  background: rgba(15, 23, 42, 0.76);
  border: 1px solid rgba(255, 255, 255, 0.14);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  color: #f8fafc;
  font-size: 11.5px;
  line-height: 1.2;
}

.co-live-hud-item {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
}

.co-live-hud-k {
  color: #94a3b8;
  font-weight: 500;
}

.co-live-hud-v {
  color: #f8fafc;
  font-weight: 600;
}

.co-live-hud-sep {
  width: 1px;
  height: 11px;
  background: rgba(255, 255, 255, 0.18);
}

.co-live-stage-tools {
  pointer-events: auto;
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-left: auto;
}

.co-live-stage-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: 1px solid rgba(255, 255, 255, 0.16);
  background: rgba(15, 23, 42, 0.76);
  color: #f8fafc;
  border-radius: var(--co-radius-pill);
  padding: 4px 10px;
  font-size: 11.5px;
  font-weight: 500;
  cursor: pointer;
  backdrop-filter: blur(8px);
  transition: background-color 0.14s ease, opacity 0.14s ease;
}

.co-live-stage-chip:hover:not(:disabled) {
  background: rgba(30, 41, 59, 0.9);
}

.co-live-stage-chip:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.co-live-stage-chip.is-resume-audio {
  background: rgba(217, 119, 6, 0.85);
  border-color: rgba(251, 191, 36, 0.65);
  color: #fffbeb;
}

.co-live-volume-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 10px 2px 6px;
  border-radius: var(--co-radius-pill);
  background: rgba(15, 23, 42, 0.76);
  border: 1px solid rgba(255, 255, 255, 0.16);
  backdrop-filter: blur(8px);
}

.co-live-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: 9999px;
  background: transparent;
  color: #f8fafc;
  cursor: pointer;
}

.co-live-icon-btn:hover {
  background: rgba(255, 255, 255, 0.14);
}

.co-live-volume-slider {
  width: 68px;
  margin: 0 !important;
}

.co-live-volume-slider :deep(.ant-slider-rail) {
  background-color: rgba(255, 255, 255, 0.28) !important;
}

.co-live-volume-slider :deep(.ant-slider-track) {
  background-color: #60a5fa !important;
}

.co-live-volume-slider:hover :deep(.ant-slider-track) {
  background-color: #93c5fd !important;
}

.co-live-volume-slider :deep(.ant-slider-handle::after) {
  background-color: #ffffff;
  box-shadow: 0 0 0 2px #60a5fa;
}

.co-live-product-overlay {
  position: absolute;
  left: 14px;
  bottom: 14px;
  z-index: 5;
  width: min(290px, calc(100% - 28px));
  padding: 12px 14px;
  border-radius: 14px;
  background: rgba(15, 23, 42, 0.86);
  border: 1px solid rgba(255, 255, 255, 0.18);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  color: #f8fafc;
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.42);
}

.co-live-overlay-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}

.co-live-overlay-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: var(--co-radius-pill);
  background: rgba(59, 130, 246, 0.28);
  border: 1px solid rgba(147, 197, 253, 0.4);
  color: #bfdbfe;
  font-size: 11px;
  font-weight: 600;
}

.co-live-overlay-close {
  border: 0;
  background: rgba(255, 255, 255, 0.1);
  color: #e2e8f0;
  width: 22px;
  height: 22px;
  border-radius: 9999px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}

.co-live-overlay-close:hover {
  background: rgba(255, 255, 255, 0.2);
}

.co-live-overlay-title {
  font-size: 14.5px;
  font-weight: 700;
  line-height: 1.35;
  color: #ffffff;
}

.co-live-overlay-price {
  margin-top: 4px;
  font-size: 18px;
  font-weight: 700;
  color: #fbbf24;
}

.co-live-overlay-highlights {
  margin: 8px 0 0;
  padding-left: 16px;
  font-size: 12px;
  line-height: 1.5;
  color: #cbd5e1;
}

.co-live-card-pop-enter-active,
.co-live-card-pop-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.co-live-card-pop-enter-from,
.co-live-card-pop-leave-to {
  opacity: 0;
  transform: translateY(10px) scale(0.97);
}

.co-live-transcript-strip {
  padding: 12px 14px;
  border-radius: var(--co-radius-md);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-live-transcript-head {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--co-muted);
}

.co-live-transcript-empty {
  font-size: 12.5px;
  color: var(--co-muted);
}

.co-live-transcript-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-live-transcript-row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 13px;
  line-height: 1.45;
}

.co-live-transcript-role {
  flex-shrink: 0;
  padding: 1px 7px;
  border-radius: var(--co-radius-pill);
  font-size: 11px;
  font-weight: 600;
  background: var(--co-track);
  color: var(--co-secondary-text);
}

.co-live-transcript-row.is-assistant .co-live-transcript-role {
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

.co-live-transcript-row.is-director .co-live-transcript-role {
  background: var(--co-warning-bg);
  color: var(--co-warning-text);
}

.co-live-transcript-text {
  color: var(--co-text);
  word-break: break-word;
}

.co-live-controls {
  padding: 14px 16px;
  border-radius: var(--co-radius-lg);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-live-controls-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}

.co-live-mic-group {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.co-live-rms-meter {
  width: 72px;
  height: 8px;
  border-radius: var(--co-radius-pill);
  background: var(--co-track);
  border: 1px solid var(--co-border-secondary);
  overflow: hidden;
}

.co-live-rms-fill {
  height: 100%;
  width: 0%;
  border-radius: inherit;
  background: var(--co-muted);
  transition: width 0.07s linear, background-color 0.15s ease;
}

.co-live-rms-fill.is-active {
  background: var(--co-success);
}

.co-live-mode-switches {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-live-text-form {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* Right Director Inspector Sidebar */
.co-live-inspector {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}

.co-live-side-card {
  padding: 14px 16px;
  border-radius: var(--co-radius-lg);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-live-side-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.co-live-side-title {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--co-text);
}

.co-live-side-title h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
  color: var(--co-text);
}

.co-live-avatar-select-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.co-live-full-select {
  width: 100%;
  min-width: 0;
  flex: 1;
}

.co-live-avatar-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.co-live-action-wall {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 7px;
}

.co-live-action-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  min-width: 0;
  max-width: 100%;
  padding: 7px 8px;
  border-radius: var(--co-radius);
  border: 1px solid var(--co-border);
  background: var(--co-subtle);
  color: var(--co-text);
  font-size: 12.5px;
  font-weight: 500;
  cursor: pointer;
  transition:
    background-color 0.14s ease,
    border-color 0.14s ease,
    color 0.14s ease;
}

.co-live-action-btn svg {
  flex-shrink: 0;
}

.co-live-action-btn span {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-live-action-btn:hover:not(:disabled) {
  background: var(--co-soft-hover);
  border-color: var(--co-input-border);
}

.co-live-action-btn.is-active {
  background: var(--co-selected);
  border-color: var(--co-accent);
  color: var(--co-accent-strong);
  font-weight: 600;
}

.co-live-action-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.co-live-driving-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-top: 8px;
  border-top: 1px solid var(--co-border-secondary);
}

.co-live-driving-head {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  font-weight: 600;
  color: var(--co-secondary-text);
}

.co-live-driving-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-live-driving-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.co-live-driving-trigger-wrap {
  flex: 1;
  min-width: 0;
  display: flex;
}

.co-live-driving-trigger {
  flex: 1;
  justify-content: flex-start;
  min-width: 0;
}

.co-live-driving-trigger span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-live-empty-mini {
  padding: 14px 12px;
  border-radius: var(--co-radius);
  border: 1px dashed var(--co-border);
  background: var(--co-subtle);
  color: var(--co-muted);
  font-size: 12.5px;
  text-align: center;
}

.co-live-product-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 260px;
  overflow-y: auto;
}

.co-live-product-item {
  padding: 10px 11px;
  border-radius: var(--co-radius);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: border-color 0.14s ease;
}

.co-live-product-item.is-active {
  border-color: var(--co-accent);
  background: var(--co-selected);
}

.co-live-product-main {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.co-live-product-title-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.co-live-product-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-live-product-price {
  font-size: 13px;
  font-weight: 700;
  color: var(--co-warning-text);
  flex-shrink: 0;
}

.co-live-product-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.co-live-mini-tag {
  font-size: 11px;
  min-height: 19px;
  padding: 0 7px;
}

.co-live-product-ops {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.co-live-llm-segment {
  width: 100%;
}

.co-live-llm-local-box {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-live-llm-local-box p {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-live-llm-form {
  display: flex;
  flex-direction: column;
  gap: 9px;
}

.co-live-llm-key-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  flex-wrap: wrap;
}

.co-live-form-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-secondary-text);
}

.co-live-create-new-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 500;
  color: var(--co-text);
  cursor: pointer;
  user-select: none;
}

.co-live-form-grid-2 {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.co-live-llm-err {
  font-size: 12px;
  color: var(--co-danger-text);
  word-break: break-word;
}

.co-live-llm-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-live-modal-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-top: 4px;
}

.co-live-modal-desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--co-secondary-text);
}

.co-live-pack-picker {
  display: flex;
  align-items: center;
  gap: 12px;
}

.co-live-pack-thumb {
  width: 56px;
  height: 56px;
  border-radius: 10px;
  object-fit: cover;
  border: 1px solid var(--co-border);
}

@media (max-width: 680px) {
  .co-live-stage {
    min-height: 280px;
  }
}
</style>
