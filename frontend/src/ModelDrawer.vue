<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Badge as ABadge,
  Button as AButton,
  Checkbox as ACheckbox,
  Drawer as ADrawer,
  Empty as AEmpty,
  Input as AInput,
  Modal as AModal,
  Progress as AProgress,
  Select as ASelect,
  SelectOption as ASelectOption,
  Space as ASpace,
  TabPane as ATabPane,
  Tabs as ATabs,
  Tag as ATag,
} from "ant-design-vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import {
  PhArrowRight,
  PhCaretDown,
  PhCaretRight,
  PhCheck,
  PhCpu,
  PhCube,
  PhGlobe,
  PhMagnifyingGlass,
  PhPlay,
  PhPushPin,
  PhPushPinSlash,
  PhSlidersHorizontal,
  PhStop,
  PhTrash,
} from "@phosphor-icons/vue";
import HubBrowser from "./HubBrowser.vue";
import ModelVersionPicker from "./ModelVersionPicker.vue";
import RuntimeParamPresets from "./RuntimeParamPresets.vue";
import { colabOneCommand } from "./api";
import { computeColabOneModelDrawerWidth } from "./colabOneToast";
import {
  describeModelRemoteStatus,
  filterCommonModels,
  repoRemoteSummary,
  type ModelLibraryTab,
} from "./modelLibrary";
import {
  buildColabOneGpuTargetOptions,
  estimateColabOneModelVramMib,
  evaluateColabOneModelDiskFit,
  evaluateColabOneTargetDiskFit,
  evaluateColabOneVramFit,
  formatColabOneGb,
  hasLiveRuntimeGpu,
  resolveColabOneDefaultGpuTarget,
  resolveColabOneGpuTarget,
  type ColabOneFileDiskState,
  type ColabOneGpuTargetId,
  type ColabOneHubFile,
  type ColabOneTargetDiskFit,
  type ColabOneVramFit,
} from "./modelCompatibility";
import {
  canColabOneLoadModel,
  type CommonRepo,
  type ModelSpec,
  type ColabOneSnapshot,
} from "./types";
import { t, tb } from "./i18n";

const props = defineProps<{
  open: boolean;
  state: ColabOneSnapshot;
  busy?: boolean;
  loading?: boolean;
}>();

const DRAWER_SKELETON_ROWS = [
  { titleW: "46%", tagW: 56, subW: "68%" },
  { titleW: "54%", tagW: 64, subW: "60%" },
  { titleW: "40%", tagW: 52, subW: "52%" },
] as const;

const emit = defineEmits<{
  "update:open": [boolean];
  device: [];
  comfyui: [];
  sync: [];
  notify: [string];
  error: [string];
}>();

const localBusy = ref(false);
const isBusy = computed(() => localBusy.value || !!props.busy);

const viewportWidth = ref(
  typeof window !== "undefined" ? window.innerWidth : 1280,
);
const drawerWidth = computed(() =>
  computeColabOneModelDrawerWidth(viewportWidth.value),
);

function onWindowResize() {
  viewportWidth.value = window.innerWidth;
}

const ready = computed(() => props.state.runtime.state === "ready");
const gpuStarting = computed(() => props.state.runtime.state === "starting");
const task = computed(() =>
  props.state.runtime.tasks?.find((t) => t.state === "running"),
);

const search = ref("");
const modelTab = ref<ModelLibraryTab>("common");
const deleteTarget = ref<ModelSpec | null>(null);
const modelOptions = ref<
  Record<string, { context_length: number; temperature: number }>
>({});
const configModelId = ref("");
const hubTargetQuery = ref("");

// Shared GPU VRAM compatibility filter (syncs across "我的" and "社区")
const manualGpuTarget = ref<ColabOneGpuTargetId | null>(null);
const onlyRunnable = ref(false);

const gpuTargetOptions = computed(() =>
  buildColabOneGpuTargetOptions(props.state.runtime),
);

const selectedGpuId = computed<ColabOneGpuTargetId>(() => {
  if (manualGpuTarget.value) {
    if (
      manualGpuTarget.value === "CURRENT" &&
      !hasLiveRuntimeGpu(props.state.runtime)
    ) {
      return resolveColabOneDefaultGpuTarget(
        props.state.runtime,
        props.state.settings.gpu,
      );
    }
    return manualGpuTarget.value;
  }
  return resolveColabOneDefaultGpuTarget(
    props.state.runtime,
    props.state.settings.gpu,
  );
});

const targetGpu = computed(() =>
  resolveColabOneGpuTarget(
    selectedGpuId.value,
    props.state.runtime,
    props.state.settings.gpu,
  ),
);

const liveGpuTarget = computed(() =>
  hasLiveRuntimeGpu(props.state.runtime)
    ? resolveColabOneGpuTarget("CURRENT", props.state.runtime)
    : null,
);

function onGpuTargetChange(val: unknown) {
  const next = String(val || "") as ColabOneGpuTargetId;
  if (next) {
    manualGpuTarget.value = next;
    if (next === "ALL") onlyRunnable.value = false;
  }
}

// Inline version picker for saved repositories in "我的"
const expandedRepo = ref("");
const repoFiles = ref<ColabOneHubFile[]>([]);
const repoRevision = ref("");
const repoModality = ref<"text" | "image" | "video">("text");
const repoComfyOnly = ref(false);
const repoSelectedFile = ref("");
const repoFilesLoading = ref(false);
const repoFilesError = ref("");
const repoInstalling = ref(false);
const repoContextLength = ref(8192);
const repoTemperature = ref(0.7);
let repoFileGen = 0;

watch(
  () => props.open,
  (isOpen) => {
    if (!isOpen) {
      configModelId.value = "";
      hubTargetQuery.value = "";
      deleteTarget.value = null;
      manualGpuTarget.value = null;
      onlyRunnable.value = false;
      modelOptions.value = {};
      resetRepoPicker();
    }
  },
);

watch(
  () => props.state.runtime.id,
  () => {
    modelOptions.value = {};
  },
);

watch(
  () => [
    props.state.runtime.loaded_model,
    props.state.runtime.active_options?.context_length,
    props.state.runtime.active_options?.temperature,
  ],
  ([loadedId]) => {
    if (
      typeof loadedId === "string" &&
      loadedId &&
      configModelId.value !== loadedId
    ) {
      delete modelOptions.value[loadedId];
    }
  },
);

function resetRepoPicker() {
  ++repoFileGen;
  expandedRepo.value = "";
  repoFiles.value = [];
  repoRevision.value = "";
  repoModality.value = "text";
  repoComfyOnly.value = false;
  repoSelectedFile.value = "";
  repoFilesLoading.value = false;
  repoFilesError.value = "";
  repoContextLength.value = 8192;
  repoTemperature.value = 0.7;
}

const isModelLoaded = (model: ModelSpec) =>
  props.state.runtime.state !== "stopped" &&
  !!(model.loaded || props.state.runtime.loaded_model === model.id);

const activeVramModel = computed(() =>
  props.state.runtime.state === "stopped"
    ? undefined
    : props.state.models.find(
        (m) => m.loaded || m.id === props.state.runtime.loaded_model,
      ),
);

const activeContextK = computed(() =>
  Math.round(
    (props.state.runtime.active_options?.context_length ||
      activeVramModel.value?.context_length ||
      8192) / 1024,
  ),
);

const activeTemp = computed(() =>
  (
    props.state.runtime.active_options?.temperature ??
    activeVramModel.value?.temperature ??
    0.7
  ).toFixed(1),
);

const modelRunningTask = (model: ModelSpec) =>
  props.state.runtime.state === "stopped"
    ? undefined
    : props.state.runtime.tasks?.find(
        (t) => t.state === "running" && t.model_id === model.id,
      );

const myModels = computed(() =>
  filterCommonModels(props.state.models, "", props.state.runtime),
);
const myTotal = computed(
  () => myModels.value.length + (props.state.common_repos?.length || 0),
);

const filtered = computed(() =>
  filterCommonModels(props.state.models, search.value, props.state.runtime),
);

const filteredMyRepos = computed<CommonRepo[]>(() => {
  const q = search.value.trim().toLowerCase();
  return (props.state.common_repos || []).filter((bm) =>
    (bm.name + " " + bm.repo + " " + (bm.note || "")).toLowerCase().includes(q),
  );
});

const bytes = (n = 0) =>
  n >= 1024 ** 3
    ? (n / 1024 ** 3).toFixed(1) + " GB"
    : (n / 1024 ** 2).toFixed(0) + " MB";

const duration = (seconds: number | null) => {
  if (seconds === null) return t("common.calculating");
  const whole = Math.ceil(Math.max(0, seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
};

function customRepoNote(note?: string): string {
  const trimmed = (note || "").trim();
  if (!trimmed || trimmed === "默认常用模型" || trimmed === "默认模型") {
    return "";
  }
  return tb(trimmed);
}

function conciseStatusBadge(model: ModelSpec): {
  label: string;
  color: "success" | "processing" | "warning" | "blue";
  dot?: boolean;
} | null {
  const status = describeModelRemoteStatus(model, props.state.runtime);
  if (status.state === "loaded") {
    return { label: t("models.badgeInUse"), color: "success", dot: true };
  }
  if (status.state === "loading") {
    return { label: t("models.badgeLoading"), color: "warning" };
  }
  if (status.state === "downloading") {
    const pct = Math.round((status.progress || 0) * 100);
    return { label: t("models.badgeDownloadingPct", { pct }), color: "processing" };
  }
  if (status.state === "downloaded") {
    return { label: t("models.badgeDownloaded"), color: "blue" };
  }
  return null;
}

function modelFitTagColor(fit: ColabOneVramFit): string {
  if (fit.status === "comfortable") return "success";
  if (fit.status === "tight") return "warning";
  if (fit.status === "hybrid" || fit.status === "heavy_hybrid") {
    return "default";
  }
  return "blue";
}

function vramNotice(
  model: ModelSpec,
): { type: "info" | "warning"; message: string } | null {
  const gpu = props.state.runtime.gpu;
  if (
    props.state.runtime.state === "stopped" ||
    !gpu?.total_mib ||
    isModelLoaded(model) ||
    !liveGpuTarget.value
  ) {
    return null;
  }
  const ctx =
    modelOptions.value[model.id]?.context_length || model.context_length || 8192;
  const needMib = estimateColabOneModelVramMib(model, ctx);
  if (!needMib) return null;
  const fit = evaluateColabOneVramFit(needMib, liveGpuTarget.value);
  if (!fit.requiresHybrid) return null;
  const haveGb = (gpu.total_mib / 1024).toFixed(1).replace(/\.0$/, "");
  const needGb = (needMib / 1024).toFixed(1).replace(/\.0$/, "");
  if (fit.status === "hybrid") {
    return {
      type: "info",
      message:
        fit.overflowGb > 0
          ? t("models.warnVramHybrid", { have: haveGb, need: needGb })
          : t("models.pickerHybridNearLimitAlert", {
              gpu: liveGpuTarget.value.shortLabel,
            }),
    };
  }
  return {
    type: "warning",
    message: t("models.warnVramLow", { have: haveGb, need: needGb }),
  };
}

function modelDiskState(model: ModelSpec): ColabOneFileDiskState {
  if (props.state.runtime.state === "stopped") {
    return {};
  }
  const running = modelRunningTask(model);
  if (model.downloaded || isModelLoaded(model) || running?.kind === "load") {
    return { downloaded: true };
  }
  const dlBytes =
    running?.kind === "download"
      ? running.download?.downloaded_bytes ||
        Math.round(Math.min(1, running.progress || 0) * (model.size || 0))
      : 0;
  const cachedBytes = Math.max(model.cached_bytes || 0, dlBytes);
  return cachedBytes > 0 ? { cachedBytes } : {};
}

const modelFitById = computed(() => {
  const map = new Map<string, ColabOneVramFit | null>();
  for (const model of filtered.value) {
    const ctx =
      modelOptions.value[model.id]?.context_length || model.context_length || 8192;
    const estMib = estimateColabOneModelVramMib(model, ctx);
    map.set(
      model.id,
      estMib > 0 ? evaluateColabOneVramFit(estMib, targetGpu.value) : null,
    );
  }
  return map;
});

function modelFitForTarget(model: ModelSpec): ColabOneVramFit | null {
  if (modelFitById.value.has(model.id)) {
    return modelFitById.value.get(model.id) ?? null;
  }
  const ctx =
    modelOptions.value[model.id]?.context_length || model.context_length || 8192;
  const estMib = estimateColabOneModelVramMib(model, ctx);
  if (estMib <= 0) return null;
  return evaluateColabOneVramFit(estMib, targetGpu.value);
}

const modelDiskFitById = computed(() => {
  const map = new Map<string, ColabOneTargetDiskFit | null>();
  for (const model of filtered.value) {
    const running = modelRunningTask(model);
    const taskTotal =
      running?.kind === "download" ? running.download?.total_bytes || 0 : 0;
    const effectiveSize = taskTotal || model.size || 0;
    map.set(
      model.id,
      effectiveSize > 0
        ? evaluateColabOneTargetDiskFit(
            effectiveSize,
            targetGpu.value,
            modelDiskState(model),
          )
        : null,
    );
  }
  return map;
});

function modelDiskFitForTarget(model: ModelSpec): ColabOneTargetDiskFit | null {
  if (modelDiskFitById.value.has(model.id)) {
    return modelDiskFitById.value.get(model.id) ?? null;
  }
  const running = modelRunningTask(model);
  const taskTotal =
    running?.kind === "download" ? running.download?.total_bytes || 0 : 0;
  const effectiveSize = taskTotal || model.size || 0;
  if (effectiveSize <= 0) return null;
  return evaluateColabOneTargetDiskFit(
    effectiveSize,
    targetGpu.value,
    modelDiskState(model),
  );
}

function modelDiskTagColor(disk: ColabOneTargetDiskFit): string {
  if (disk.status === "exceeds_vm") return "error";
  if (disk.status === "insufficient_live" || disk.status === "tight") {
    return "warning";
  }
  return "default";
}

function diskNotice(
  model: ModelSpec,
): { type: "error" | "warning"; message: string } | null {
  const fit = modelDiskFitForTarget(model);
  const type = fit?.status === "exceeds_vm" ? "error" : "warning";
  if (
    props.state.runtime.state !== "stopped" &&
    selectedGpuId.value === "CURRENT"
  ) {
    const liveWarning = evaluateColabOneModelDiskFit(
      model,
      props.state.runtime,
    ).warning;
    if (liveWarning) return { type, message: liveWarning };
    if (fit && fit.insufficient && fit.detail) {
      return { type, message: fit.detail };
    }
    return null;
  }
  return fit && fit.insufficient && fit.detail
    ? { type, message: fit.detail }
    : null;
}

function repoFileDiskState(
  repo: string,
  fileName: string,
): ColabOneFileDiskState | undefined {
  const summary = repoRemoteSummary(repo, props.state.models, props.state.runtime);
  const matches = summary.savedModels.filter((m) =>
    m.files?.some((f) => f.path === fileName),
  );
  if (!matches.length) return undefined;
  const matched =
    matches.find(
      (m) => m.loaded || props.state.runtime.loaded_model === m.id,
    ) ||
    matches.find((m) => m.downloaded) ||
    matches[0];
  if (!matched) return undefined;
  return modelDiskState(matched);
}

function loadedModelForRepo(repo: string): ModelSpec | undefined {
  if (props.state.runtime.state === "stopped") return undefined;
  const summary = repoRemoteSummary(repo, props.state.models, props.state.runtime);
  return summary.savedModels.find(
    (m) => m.loaded || (!!m.id && props.state.runtime.loaded_model === m.id),
  );
}

function repoFileSavedTag(repo: string, fileName: string): string {
  const summary = repoRemoteSummary(repo, props.state.models, props.state.runtime);
  const matches = summary.savedModels.filter((m) =>
    m.files?.some((f) => f.path === fileName),
  );
  if (!matches.length) return "";
  const matched =
    matches.find(
      (m) => m.loaded || props.state.runtime.loaded_model === m.id,
    ) ||
    matches.find((m) => m.downloaded) ||
    matches[0];
  if (!matched) return "";
  if (
    matched.downloaded ||
    (props.state.runtime.state !== "stopped" &&
      (matched.loaded || props.state.runtime.loaded_model === matched.id))
  ) {
    return t("models.badgeDownloaded");
  }
  return matched.common ? t("models.badgeInMine") : "";
}

const downloadDisabledReason = computed(() => {
  if (!ready.value) return t("models.disabledStartGpu");
  if (props.state.operation) return tb(props.state.operation.title);
  if (task.value) return t("models.disabledWaitTask");
  if (!props.state.runtime.protocol) return t("models.disabledSyncingRemote");
  return "";
});

const loadDisabledReason = computed(() => {
  if (!ready.value) return t("models.disabledStartGpu");
  if (props.state.operation) return tb(props.state.operation.title);
  if (task.value) return t("models.disabledWaitTask");
  return "";
});

function modelActionDisabledReason(_model: ModelSpec, isDownload: boolean): string {
  return isDownload ? downloadDisabledReason.value : loadDisabledReason.value;
}

async function act(action: string, payload: Record<string, unknown> = {}) {
  localBusy.value = true;
  try {
    const res = await colabOneCommand(action, payload);
    emit("sync");
    return res;
  } catch (e) {
    emit("error", String(e));
    return null;
  } finally {
    localBusy.value = false;
  }
}

function closeDrawer() {
  emit("update:open", false);
}

function onDrawerOpenChange(next: boolean) {
  emit("update:open", next);
}

function onModelTabChange(raw: unknown) {
  hubTargetQuery.value = "";
  modelTab.value = raw === "hub" ? "hub" : "common";
}

function searchInCommunity(keyword: string) {
  hubTargetQuery.value = keyword;
  modelTab.value = "hub";
}

async function toggleRepoVersions(repo: string, forceOpen = false) {
  if (!forceOpen && expandedRepo.value === repo) {
    resetRepoPicker();
    return;
  }
  const gen = ++repoFileGen;
  expandedRepo.value = repo;
  repoFiles.value = [];
  repoRevision.value = "";
  repoModality.value = "text";
  repoComfyOnly.value = false;
  repoSelectedFile.value = "";
  repoFilesError.value = "";
  repoFilesLoading.value = true;
  try {
    const result = await colabOneCommand("hub.files", { repo });
    if (gen !== repoFileGen) return;
    repoRevision.value = result.revision || "";
    repoModality.value = result.modality || "text";
    repoComfyOnly.value =
      typeof result.comfy_only === "boolean"
        ? result.comfy_only
        : repoModality.value === "image" || repoModality.value === "video";
    repoFiles.value = result.files || [];
  } catch (e) {
    if (gen === repoFileGen) repoFilesError.value = String(e);
  } finally {
    if (gen === repoFileGen) repoFilesLoading.value = false;
  }
}

async function installFromMyRepo(mode: "load" | "download" | "save") {
  if (
    !expandedRepo.value ||
    !repoSelectedFile.value ||
    repoInstalling.value ||
    repoComfyOnly.value
  ) {
    return;
  }
  if (mode !== "save" && (!ready.value || !!task.value || isBusy.value)) {
    return;
  }
  const gen = repoFileGen;
  repoInstalling.value = true;
  repoFilesError.value = "";
  try {
    const payload: Record<string, unknown> = {
      repo: expandedRepo.value,
      filename: repoSelectedFile.value,
      revision: repoRevision.value,
      download: mode !== "save",
    };
    if (mode === "load") {
      payload.load = true;
      payload.context_length = repoContextLength.value;
      payload.temperature = repoTemperature.value;
    }
    await colabOneCommand("hub.install", payload);
    if (gen === repoFileGen) {
      resetRepoPicker();
    }
    emit("sync");
    emit(
      "notify",
      mode === "load"
        ? t("models.toastDownloadAndLoad")
        : mode === "download"
          ? t("models.toastDownloadingRemote")
          : t("models.toastSavedVersionMine"),
    );
  } catch (e) {
    if (gen === repoFileGen) repoFilesError.value = String(e);
  } finally {
    repoInstalling.value = false;
  }
}

function optsFor(model: ModelSpec) {
  let entry = modelOptions.value[model.id];
  if (!entry) {
    entry = {
      context_length:
        (isModelLoaded(model) &&
          props.state.runtime.active_options?.context_length) ||
        model.context_length ||
        8192,
      temperature:
        (isModelLoaded(model)
          ? props.state.runtime.active_options?.temperature
          : undefined) ??
        model.temperature ??
        0.7,
    };
    modelOptions.value[model.id] = entry;
  }
  return entry;
}

function setModelContextLength(model: ModelSpec, raw: unknown) {
  const parsed = Number(raw);
  if (Number.isFinite(parsed) && parsed > 0) {
    optsFor(model).context_length = parsed;
  }
}

function setModelTemperature(model: ModelSpec, raw: unknown) {
  const num = Array.isArray(raw) ? Number(raw[0]) : Number(raw);
  if (Number.isFinite(num)) {
    const clamped = Math.min(2, Math.max(0, Math.round(num * 10) / 10));
    optsFor(model).temperature = clamped;
  }
}

function toggleConfig(model: ModelSpec) {
  if (configModelId.value === model.id) {
    configModelId.value = "";
  } else {
    optsFor(model);
    configModelId.value = model.id;
  }
}

async function toggleModelMine(model: ModelSpec) {
  const res = await act("model.common", {
    id: model.id,
    common: !model.common,
  });
  if (res) {
    emit(
      "notify",
      res.common
        ? t("models.toastAddedToMine", { name: model.name })
        : t("models.toastRemovedFromMine", { name: model.name }),
    );
  }
}

async function removeMyRepo(repo: CommonRepo) {
  if (expandedRepo.value === repo.repo) resetRepoPicker();
  const res = await act("common_repo.remove", { repo: repo.repo });
  if (res) {
    emit("notify", t("models.toastRemovedFromMine", { name: repo.repo }));
  }
}

async function downloadModel(model: ModelSpec, loadAfter: boolean) {
  const payload: Record<string, unknown> = { id: model.id };
  if (loadAfter) {
    payload.load = true;
    const opts = optsFor(model);
    payload.context_length = opts.context_length;
    payload.temperature = opts.temperature;
  }
  const res = await act("model.download", payload);
  if (res) {
    if (loadAfter) configModelId.value = "";
    emit(
      "notify",
      loadAfter
        ? t("models.toastDownloadAndLoad")
        : t("models.toastDownloadingModel"),
    );
  }
}

async function loadModel(model: ModelSpec) {
  const opts = optsFor(model);
  const payload: Record<string, unknown> = {
    id: model.id,
    context_length: opts.context_length,
    temperature: opts.temperature,
  };
  const res = await act("model.load", payload);
  if (res) {
    configModelId.value = "";
    emit("notify", t("models.toastLoadingVram"));
  }
}

async function unloadModel() {
  await act("model.unload");
}

async function confirmDeleteModel() {
  if (!deleteTarget.value) return;
  const targetId = deleteTarget.value.id;
  deleteTarget.value = null;
  if (await act("model.delete", { id: targetId })) {
    emit("notify", t("models.toastDeletingRemote"));
  }
}

function onHubRepoUpdated(message: string) {
  emit("sync");
  emit("notify", message);
}

function onHubInstalled(mode: "save" | "download" | "load") {
  hubTargetQuery.value = "";
  modelTab.value = "common";
  emit("sync");
  emit(
    "notify",
    mode === "save"
      ? t("models.toastAddedMineShort")
      : mode === "load"
        ? t("models.toastDownloadAndLoad")
        : t("models.toastDownloadingRemote"),
  );
}

function handleDrawerKeydown(e: KeyboardEvent) {
  if (!props.open) return;
  if (e.key === "Escape") {
    e.stopImmediatePropagation();
    if (deleteTarget.value) {
      deleteTarget.value = null;
    } else if (configModelId.value) {
      configModelId.value = "";
    } else if (expandedRepo.value) {
      resetRepoPicker();
    } else {
      closeDrawer();
    }
  }
}

onMounted(() => {
  window.addEventListener("resize", onWindowResize);
  window.addEventListener("keydown", handleDrawerKeydown);
});

function goDevice() {
  closeDrawer();
  emit("device");
}

function goComfyui() {
  closeDrawer();
  emit("comfyui");
}

onUnmounted(() => {
  window.removeEventListener("resize", onWindowResize);
  window.removeEventListener("keydown", handleDrawerKeydown);
});
</script>

<template>
  <ADrawer
    :open="open"
    placement="right"
    :width="drawerWidth"
    :mask="true"
    :mask-closable="true"
    :keyboard="false"
    :destroy-on-close="true"
    :z-index="1010"
    :body-style="{
      padding: '12px 20px 24px',
      overflowY: 'auto',
    }"
    root-class-name="co-model-drawer-root"
    @update:open="onDrawerOpenChange"
  >
    <template #title>
      <div class="co-model-drawer-title">
        <PhCube :size="18" weight="duotone" class="co-model-drawer-icon" />
        <span>{{ t("models.drawerTitle") }}</span>
      </div>
    </template>

    <div
      class="co-model-drawer-body co-models-drawer"
      role="region"
      :aria-label="t('models.drawerAria')"
    >
      <!-- Slim Active Status Strip -->
      <div
        v-if="loading && !activeVramModel"
        class="co-subtle-hint co-gpu-hint-bar co-model-strip-skeleton"
        aria-hidden="true"
      >
        <ColabOneSkeleton width="54%" :height="14" variant="text" />
        <ColabOneSkeleton
          :width="82"
          :height="14"
          variant="text"
          :delay="80"
        />
      </div>

      <div v-else-if="activeVramModel" class="co-active-bar co-vram-console">
        <div class="co-active-bar-info">
          <ABadge status="processing" color="#52c41a" />
          <strong class="co-active-bar-name">
            {{ t("models.activeModelBanner", { name: activeVramModel.name }) }}
          </strong>
          <span class="co-active-bar-spec co-tabular">
            {{ t("api.contextAndTemp", { ctx: activeContextK, temp: activeTemp }) }}
          </span>
        </div>
        <ASpace :size="6">
          <AButton type="primary" size="small" @click="closeDrawer">
            <template #icon><PhCheck :size="13" /></template>
            {{ t("models.useThisModel") }}
          </AButton>
          <AButton
            size="small"
            :disabled="!!task || isBusy"
            @click="unloadModel"
          >
            {{ t("models.unload") }}
          </AButton>
        </ASpace>
      </div>

      <div v-else-if="!ready" class="co-subtle-hint co-gpu-hint-bar">
        <span>
          {{
            gpuStarting
              ? t("models.offlineStartingNotice")
              : t("models.offlineStoppedNotice")
          }}
        </span>
        <AButton
          type="link"
          size="small"
          class="co-inline-link co-gpu-hint-link"
          @click="goDevice"
        >
          {{ gpuStarting ? t("api.viewGpuProgress") : t("api.goStartGpu") }}
          <PhArrowRight :size="13" />
        </AButton>
      </div>

      <div
        v-else-if="state.runtime.comfyui?.running"
        class="co-subtle-hint"
      >
        {{ t("models.comfyMutexNotice") }}
      </div>

      <!-- Two Primary Entries (Mine & Community) + Shared GPU VRAM Target Selector -->
      <div class="co-model-tabs-bar co-model-tabs">
        <ATabs
          :active-key="modelTab"
          class="co-tabs-inline"
          @change="onModelTabChange"
        >
          <ATabPane key="common">
            <template #tab>
              <span class="co-tab-label">
                <span>{{ t("models.tabMine") }}</span>
                <span v-if="myTotal" class="co-tab-count co-tabular">
                  {{ myTotal }}
                </span>
              </span>
            </template>
          </ATabPane>
          <ATabPane key="hub" :tab="t('models.tabHub')" />
        </ATabs>

        <div class="co-gpu-adapter-controls">
          <span class="co-gpu-adapter-label">
            <PhCpu :size="14" class="co-gpu-adapter-icon" />
            <span>{{ t("models.vramFilterLabel") }}</span>
          </span>
          <ASelect
            :value="selectedGpuId"
            size="small"
            :aria-label="t('models.vramFilterAria')"
            option-label-prop="label"
            class="co-gpu-target-select"
            :dropdown-match-select-width="320"
            @change="onGpuTargetChange"
          >
            <ASelectOption
              v-for="opt in gpuTargetOptions"
              :key="opt.id"
              :value="opt.id"
              :label="opt.label"
            >
              <div class="co-gpu-opt-row">
                <span class="co-gpu-opt-main">
                  <ABadge v-if="opt.isLive" status="success" />
                  <span>{{ opt.label }}</span>
                </span>
                <span class="co-gpu-opt-sub">{{ opt.subtitle }}</span>
              </div>
            </ASelectOption>
          </ASelect>

          <ACheckbox
            v-if="modelTab === 'hub' && targetGpu.vramMib > 0"
            v-model:checked="onlyRunnable"
            class="co-only-runnable-check"
          >
            {{ t("models.onlyRunnable") }}
          </ACheckbox>
        </div>
      </div>

      <!-- Entry 2: Community -->
      <HubBrowser
        v-if="modelTab === 'hub'"
        :ready="ready"
        :disabled="
          !!task || isBusy || !!state.operation || !state.runtime.protocol
        "
        :download-disabled-reason="downloadDisabledReason"
        :common-repos="state.common_repos"
        :initial-query="hubTargetQuery"
        :models="state.models"
        :runtime="state.runtime"
        :target-gpu="targetGpu"
        :only-runnable="onlyRunnable"
        @common-repo-updated="onHubRepoUpdated"
        @installed="onHubInstalled"
        @toggle-common="toggleModelMine"
        @use-loaded="closeDrawer"
        @switch-gpu="onGpuTargetChange"
        @clear-only-runnable="onlyRunnable = false"
        @go-comfyui="goComfyui"
      />

      <!-- Entry 1: Mine -->
      <template v-else>
        <div
          v-if="myTotal > 0"
          class="co-list-toolbar"
        >
          <AInput
            v-model:value="search"
            type="search"
            allow-clear
            class="co-model-search"
            :placeholder="t('models.searchMinePlaceholder')"
            :aria-label="t('models.searchMineAria')"
          >
            <template #prefix>
              <PhMagnifyingGlass :size="15" class="co-input-prefix" />
            </template>
          </AInput>
        </div>

        <!-- Initial Snapshot Skeleton for My Models -->
        <div
          v-if="loading && !filtered.length && !filteredMyRepos.length"
          class="co-model-list co-model-skeleton-list"
          role="status"
          aria-busy="true"
        >
          <span class="co-sr-only">{{ t("common.loading") }}</span>
          <div
            v-for="(row, idx) in DRAWER_SKELETON_ROWS"
            :key="idx"
            class="co-model-row co-model-row-skeleton"
          >
            <div class="co-model-row-main">
              <div class="co-model-info">
                <div class="co-model-title-row">
                  <ColabOneSkeleton
                    :width="row.titleW"
                    :height="16"
                    variant="text"
                    :delay="idx * 70"
                  />
                  <ColabOneSkeleton
                    :width="row.tagW"
                    :height="18"
                    variant="tag"
                    tone="subtle"
                    :delay="idx * 70 + 45"
                  />
                </div>
                <div class="co-model-sub">
                  <ColabOneSkeleton
                    :width="row.subW"
                    :height="13"
                    variant="text"
                    tone="subtle"
                    :delay="idx * 70 + 85"
                  />
                </div>
              </div>
              <div class="co-model-actions co-model-sk-actions">
                <ColabOneSkeleton
                  :width="86"
                  :height="24"
                  variant="button"
                  :radius="6"
                  :delay="idx * 70 + 95"
                />
                <ColabOneSkeleton
                  :width="24"
                  :height="24"
                  variant="circle"
                  tone="subtle"
                  :delay="idx * 70 + 130"
                />
                <ColabOneSkeleton
                  :width="24"
                  :height="24"
                  variant="circle"
                  tone="subtle"
                  :delay="idx * 70 + 160"
                />
              </div>
            </div>
          </div>
        </div>

        <!-- My Models + My Saved Repos Unified Clean List -->
        <div
          v-else-if="filtered.length || filteredMyRepos.length"
          class="co-model-list co-sk-reveal"
        >
          <div
            v-for="model in filtered"
            :key="model.id"
            class="co-model-row co-model-card"
            :class="{
              loaded: isModelLoaded(model),
              downloading: modelRunningTask(model)?.kind === 'download',
              loading: modelRunningTask(model)?.kind === 'load',
              'is-overflow':
                !isModelLoaded(model) &&
                (modelFitForTarget(model)?.status === 'heavy_hybrid' ||
                  !!modelDiskFitForTarget(model)?.exceedsVm),
            }"
          >
            <div class="co-model-row-main">
              <div class="co-model-info">
                <div class="co-model-title-row">
                  <h3 class="co-model-name">{{ model.name }}</h3>
                  <ATag
                    v-if="conciseStatusBadge(model)"
                    :color="conciseStatusBadge(model)!.color"
                    :bordered="false"
                    class="co-model-remote-pill"
                  >
                    <ABadge
                      v-if="conciseStatusBadge(model)!.dot"
                      status="processing"
                      color="#52c41a"
                    />
                    <span>{{ conciseStatusBadge(model)!.label }}</span>
                  </ATag>
                  <ATag
                    v-if="modelFitForTarget(model)"
                    :color="modelFitTagColor(modelFitForTarget(model)!)"
                    :bordered="false"
                    class="co-model-fit-pill"
                  >
                    {{ modelFitForTarget(model)!.label }}
                  </ATag>
                  <ATag
                    v-if="modelDiskFitForTarget(model)?.label"
                    :color="modelDiskTagColor(modelDiskFitForTarget(model)!)"
                    :bordered="false"
                    class="co-model-fit-pill"
                  >
                    {{ modelDiskFitForTarget(model)!.label }}
                  </ATag>
                </div>

                <div class="co-model-sub co-tabular">
                  <span
                    :class="{
                      'co-disk-danger':
                        modelDiskFitForTarget(model)?.status === 'exceeds_vm',
                      'co-disk-warn':
                        modelDiskFitForTarget(model)?.status ===
                          'insufficient_live' ||
                        modelDiskFitForTarget(model)?.status === 'tight',
                    }"
                  >
                    <template v-if="model.size > 0">
                      {{ t("models.estimatedDisk", { size: bytes(model.size) }) }}<template
                        v-if="modelDiskFitForTarget(model)?.status === 'cached'"
                      >{{ t("models.pickerDiskCachedInline") }}</template><template
                        v-else-if="targetGpu.diskFreeGb > 0 || targetGpu.isLiveDisk"
                      >{{ " " }}{{
                          targetGpu.isLiveDisk
                            ? t("models.pickerDiskAvailLive", {
                                avail: formatColabOneGb(targetGpu.diskFreeGb),
                              })
                            : t("models.pickerDiskAvailApprox", {
                                avail: formatColabOneGb(targetGpu.diskFreeGb),
                              })
                        }}</template>
                    </template>
                    <template v-else>
                      {{ t("common.unknownSize") }}
                    </template>
                  </span>
                  <template v-if="modelFitForTarget(model)">
                    <span>·</span>
                    <span>
                      {{
                        t("models.estimatedVram", {
                          gb: formatColabOneGb(
                            modelFitForTarget(model)!.estimatedVramGb,
                          ),
                        })
                      }}
                    </span>
                  </template>
                  <template v-if="model.description">
                    <span>·</span>
                    <span class="co-model-desc">{{ tb(model.description) }}</span>
                  </template>
                </div>
              </div>

              <div class="co-model-actions">
                <template v-if="isModelLoaded(model)">
                  <AButton
                    type="primary"
                    ghost
                    size="small"
                    @click="closeDrawer"
                  >
                    <template #icon><PhCheck :size="13" /></template>
                    {{ t("models.useThisModel") }}
                  </AButton>
                  <AButton
                    size="small"
                    :disabled="!!task || isBusy"
                    @click="unloadModel"
                  >
                    {{ t("models.unload") }}
                  </AButton>
                </template>

                <template
                  v-else-if="modelRunningTask(model)?.kind === 'download'"
                >
                  <AButton
                    danger
                    size="small"
                    :disabled="isBusy"
                    @click="
                      act('task.cancel', {
                        id: modelRunningTask(model)!.id,
                      })
                    "
                  >
                    <template #icon><PhStop :size="13" weight="fill" /></template>
                    {{ t("models.cancelDownload") }}
                  </AButton>
                </template>

                <template v-else-if="model.downloaded">
                  <ColabOneTooltip :title="modelActionDisabledReason(model, false)">
                    <AButton
                      type="primary"
                      size="small"
                      :disabled="
                        !canColabOneLoadModel(model, state) || isBusy
                      "
                      @click="loadModel(model)"
                    >
                      <template #icon><PhPlay :size="13" weight="fill" /></template>
                      {{ t("models.load") }}
                    </AButton>
                  </ColabOneTooltip>
                </template>

                <template v-else>
                  <ColabOneTooltip :title="modelActionDisabledReason(model, true)">
                    <AButton
                      type="primary"
                      size="small"
                      :disabled="
                        !ready ||
                        !!task ||
                        isBusy ||
                        !!state.operation ||
                        !state.runtime.protocol
                      "
                      @click="downloadModel(model, true)"
                    >
                      <template #icon><PhPlay :size="13" weight="fill" /></template>
                      {{ t("models.downloadAndLoad") }}
                    </AButton>
                  </ColabOneTooltip>
                </template>

                <!-- Quiet Secondary Icon Controls -->
                <ColabOneTooltip
                  v-if="modelRunningTask(model)?.kind !== 'download'"
                  :title="t('models.paramsTooltip')"
                >
                  <AButton
                    type="text"
                    size="small"
                    class="co-icon-btn"
                    :class="{ active: configModelId === model.id }"
                    :aria-label="t('models.paramsAria')"
                    :aria-expanded="configModelId === model.id"
                    @click="toggleConfig(model)"
                  >
                    <template #icon>
                      <PhSlidersHorizontal :size="15" />
                    </template>
                  </AButton>
                </ColabOneTooltip>

                <ColabOneTooltip
                  :title="
                    model.common
                      ? t('models.removeFromMine')
                      : t('models.addToMine')
                  "
                >
                  <AButton
                    type="text"
                    size="small"
                    class="co-icon-btn"
                    :aria-label="
                      model.common
                        ? t('models.removeFromMineTarget', { name: model.name })
                        : t('models.addToMineTarget', { name: model.name })
                    "
                    :disabled="isBusy"
                    @click="toggleModelMine(model)"
                  >
                    <template #icon>
                      <PhPushPinSlash v-if="model.common" :size="15" />
                      <PhPushPin v-else :size="15" />
                    </template>
                  </AButton>
                </ColabOneTooltip>

                <ColabOneTooltip
                  v-if="model.downloaded && !isModelLoaded(model)"
                  :title="t('models.deleteRemoteTooltip')"
                >
                  <AButton
                    type="text"
                    danger
                    size="small"
                    class="co-icon-btn"
                    :aria-label="t('models.deleteRemoteTooltip')"
                    :disabled="!!task || isBusy"
                    @click="deleteTarget = model"
                  >
                    <template #icon>
                      <PhTrash :size="15" />
                    </template>
                  </AButton>
                </ColabOneTooltip>

                <ColabOneTooltip
                  v-else-if="model.custom && !model.downloaded"
                  :title="t('models.removeLocalTooltip')"
                >
                  <AButton
                    type="text"
                    danger
                    size="small"
                    class="co-icon-btn"
                    :aria-label="t('models.removeLocalAria')"
                    :disabled="isBusy"
                    @click="act('model.remove_local', { id: model.id })"
                  >
                    <template #icon>
                      <PhTrash :size="15" />
                    </template>
                  </AButton>
                </ColabOneTooltip>
              </div>
            </div>

            <AAlert
              v-if="vramNotice(model)"
              :type="vramNotice(model)!.type"
              show-icon
              :message="vramNotice(model)!.message"
              class="co-model-warning co-model-vram-warning"
            />
            <AAlert
              v-if="diskNotice(model)"
              :type="diskNotice(model)!.type"
              show-icon
              :message="diskNotice(model)!.message"
              class="co-model-warning"
            />

            <!-- Inline Download Progress -->
            <div
              v-if="modelRunningTask(model)?.kind === 'download'"
              class="co-inline-progress"
              role="status"
            >
              <div class="co-inline-progress-head">
                <span>
                  {{
                    modelRunningTask(model)!.download?.phase === "verifying"
                      ? t("models.verifyingIntegrity")
                      : t("models.downloadingPct", {
                          pct: Math.round(
                            (modelRunningTask(model)!.progress || 0) * 100,
                          ),
                        })
                  }}
                </span>
                <span
                  v-if="modelRunningTask(model)!.download"
                  class="co-tabular co-muted"
                >
                  {{
                    (
                      modelRunningTask(model)!.download!.speed_bps /
                      1024 ** 2
                    ).toFixed(1)
                  }}
                  MiB/s ·
                  {{
                    bytes(modelRunningTask(model)!.download!.downloaded_bytes)
                  }}
                  /
                  {{ bytes(modelRunningTask(model)!.download!.total_bytes) }}
                  <template
                    v-if="
                      modelRunningTask(model)!.download!.eta_seconds !== null
                    "
                  >
                    {{
                      t("models.etaRemaining", {
                        eta: duration(
                          modelRunningTask(model)!.download!.eta_seconds,
                        ),
                      })
                    }}
                  </template>
                </span>
              </div>
              <AProgress
                :percent="
                  Math.round((modelRunningTask(model)!.progress || 0) * 100)
                "
                size="small"
                status="active"
              />
            </div>

            <!-- Final-Stage Runtime Parameter Presets & Effect Preview -->
            <div
              v-if="configModelId === model.id"
              class="co-config-drawer co-model-config-drawer"
            >
              <RuntimeParamPresets
                :context-length="optsFor(model).context_length"
                :temperature="optsFor(model).temperature"
                :model-bytes="model.size"
                :target-gpu="liveGpuTarget || targetGpu"
                @update:context-length="(v) => setModelContextLength(model, v)"
                @update:temperature="(v) => setModelTemperature(model, v)"
              />

              <div class="co-config-actions">
                <AButton
                  v-if="isModelLoaded(model)"
                  type="primary"
                  size="small"
                  :disabled="!!task || isBusy || !!state.operation"
                  @click="loadModel(model)"
                >
                  {{ t("models.applyAndReload") }}
                </AButton>
                <AButton
                  v-else-if="model.downloaded"
                  type="primary"
                  size="small"
                  :disabled="!canColabOneLoadModel(model, state) || isBusy"
                  @click="loadModel(model)"
                >
                  <template #icon><PhPlay :size="13" weight="fill" /></template>
                  {{ t("models.loadVram") }}
                </AButton>
                <AButton
                  v-else
                  type="primary"
                  size="small"
                  :disabled="
                    !ready ||
                    !!task ||
                    isBusy ||
                    !!state.operation ||
                    !state.runtime.protocol
                  "
                  @click="downloadModel(model, true)"
                >
                  {{ t("models.downloadAndLoadWithParams") }}
                </AButton>
              </div>
            </div>
          </div>

          <!-- My Saved Community Repositories (unified beginner-friendly version picker) -->
          <div
            v-for="bm in filteredMyRepos"
            :key="bm.repo"
            class="co-model-row co-common-repo-row"
            :class="{ selected: expandedRepo === bm.repo }"
          >
            <div class="co-model-row-main">
              <div class="co-model-info">
                <div class="co-model-title-row">
                  <h3 class="co-model-name">
                    {{ bm.name || bm.repo.split("/").pop() || bm.repo }}
                  </h3>
                </div>
                <div class="co-model-sub">
                  <span>{{ bm.repo }}</span>
                  <template v-if="customRepoNote(bm.note)">
                    <span>·</span>
                    <span>{{ customRepoNote(bm.note) }}</span>
                  </template>
                </div>
              </div>
              <div class="co-model-actions">
                <AButton
                  type="primary"
                  :ghost="expandedRepo === bm.repo"
                  size="small"
                  :aria-expanded="expandedRepo === bm.repo"
                  @click="toggleRepoVersions(bm.repo)"
                >
                  {{
                    expandedRepo === bm.repo
                      ? t("common.collapse")
                      : t("models.chooseVersion")
                  }}
                  <PhCaretDown v-if="expandedRepo === bm.repo" :size="12" />
                  <PhCaretRight v-else :size="12" />
                </AButton>
                <ColabOneTooltip :title="t('models.removeFromMine')">
                  <AButton
                    type="text"
                    size="small"
                    class="co-icon-btn"
                    :aria-label="
                      t('models.removeFromMineTarget', { name: bm.repo })
                    "
                    :disabled="isBusy"
                    @click="removeMyRepo(bm)"
                  >
                    <template #icon>
                      <PhPushPinSlash :size="15" />
                    </template>
                  </AButton>
                </ColabOneTooltip>
              </div>
            </div>

            <!-- Unified Beginner-Friendly Version Picker right inside "Mine" -->
            <div
              v-if="expandedRepo === bm.repo"
              class="co-repo-picker"
              :aria-label="t('models.selectVersionAria')"
            >
              <ModelVersionPicker
                :repo="bm.repo"
                :files="repoFiles"
                :loading="repoFilesLoading"
                :error="repoFilesError"
                :modality="repoModality"
                :comfy-only="repoComfyOnly"
                :target-gpu="targetGpu"
                :runtime="state.runtime"
                :ready="ready"
                :disabled="
                  !!task || isBusy || !!state.operation || !state.runtime.protocol
                "
                :installing="repoInstalling"
                :selected-filename="repoSelectedFile"
                :context-length="repoContextLength"
                :temperature="repoTemperature"
                :show-save-button="false"
                :show-use-loaded="!!loadedModelForRepo(bm.repo)"
                :saved-tag-for-file="(fn) => repoFileSavedTag(bm.repo, fn)"
                :file-disk-state="(fn) => repoFileDiskState(bm.repo, fn)"
                :download-disabled-reason="downloadDisabledReason"
                @update:selected-filename="(v) => (repoSelectedFile = v)"
                @update:context-length="(v) => (repoContextLength = v)"
                @update:temperature="(v) => (repoTemperature = v)"
                @retry="toggleRepoVersions(bm.repo, true)"
                @install="installFromMyRepo"
                @use-loaded="closeDrawer"
                @switch-gpu="onGpuTargetChange"
                @go-comfyui="goComfyui"
              />
            </div>
          </div>
        </div>

        <!-- Empty State -->
        <div
          v-else
          class="co-empty-panel co-starter-empty"
        >
          <template v-if="search.trim()">
            <AEmpty
              :description="t('models.emptySearch', { query: search.trim() })"
            >
              <ASpace :size="8" wrap>
                <AButton size="small" @click="search = ''">
                  {{ t("models.clearSearch") }}
                </AButton>
                <AButton
                  type="primary"
                  size="small"
                  @click="searchInCommunity(search.trim())"
                >
                  {{ t("models.searchInCommunity", { query: search.trim() }) }}
                </AButton>
              </ASpace>
            </AEmpty>
          </template>
          <template v-else>
            <AEmpty :description="t('models.emptyMine')">
              <AButton
                type="primary"
                size="small"
                @click="onModelTabChange('hub')"
              >
                <template #icon><PhGlobe :size="14" /></template>
                {{ t("models.discoverInCommunity") }}
              </AButton>
            </AEmpty>
          </template>
        </div>
      </template>
    </div>

    <!-- Delete Remote Model File Confirmation -->
    <AModal
      :open="!!deleteTarget"
      :title="t('models.deleteModalTitle')"
      :width="420"
      :z-index="1050"
      :footer="null"
      centered
      @cancel="deleteTarget = null"
    >
      <div class="co-modal-stack">
        <p v-if="deleteTarget" class="co-muted co-modal-lead">
          {{ t("models.deleteModalDesc", { name: deleteTarget.name }) }}
        </p>
        <div class="co-modal-footer">
          <AButton @click="deleteTarget = null">{{ t("common.cancel") }}</AButton>
          <AButton type="primary" danger @click="confirmDeleteModel">
            {{ t("models.deleteFilesBtn") }}
          </AButton>
        </div>
      </div>
    </AModal>
  </ADrawer>
</template>

<style scoped>
.co-model-drawer-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.co-model-drawer-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-model-drawer-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}

/* Slim Active Model Strip */
.co-active-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-success-border);
  background: var(--co-success-bg);
  color: var(--co-text);
}

.co-active-bar-info {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
  flex: 1;
}

.co-active-bar-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--co-text);
}

.co-active-bar-spec {
  font-size: 12px;
  color: var(--co-muted);
}

.co-subtle-hint {
  font-size: 13px;
  color: var(--co-muted);
  padding: 2px 0;
}

.co-gpu-hint-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  padding: 8px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-gpu-hint-link {
  font-weight: 500;
  flex-shrink: 0;
}

/* 2-Entry Tabs + GPU Target Bar */
.co-model-tabs-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}

.co-model-tabs-bar :deep(.ant-tabs-nav) {
  margin-bottom: 0;
}

.co-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  line-height: 1.2;
}

.co-tab-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 9999px;
  font-size: 11px;
  font-weight: 600;
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

.co-model-tabs-bar :deep(.ant-tabs-tab-active .co-tab-count) {
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

:global(:root[data-theme="dark"])
  .co-model-tabs-bar
  :deep(.ant-tabs-tab-active .co-tab-count) {
  background: rgba(59, 130, 246, 0.24);
  color: var(--co-accent-strong);
}

.co-gpu-adapter-controls {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-gpu-adapter-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--co-muted);
  white-space: nowrap;
}

.co-gpu-adapter-icon {
  color: var(--co-accent);
}

.co-gpu-target-select {
  min-width: 158px;
}

.co-gpu-opt-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.co-gpu-opt-main {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-weight: 500;
}

.co-gpu-opt-sub {
  font-size: 11px;
  color: var(--co-muted);
}

.co-only-runnable-check {
  font-size: 12px;
  color: var(--co-secondary-text);
  white-space: nowrap;
}

.co-list-toolbar {
  width: 100%;
}

.co-model-search {
  width: 100%;
}

.co-input-prefix {
  color: var(--co-muted);
}

/* Clean Model Rows */
.co-model-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-model-row {
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  color: var(--co-text);
  display: flex;
  flex-direction: column;
  gap: 10px;
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    opacity 0.16s ease;
}

.co-model-row:hover {
  border-color: var(--co-border);
  box-shadow: var(--co-card-shadow);
}

.co-model-row.loaded {
  border-color: var(--co-success-border);
}

.co-model-row.downloading,
.co-model-row.loading,
.co-model-row.selected {
  border-color: var(--co-accent);
}

.co-model-row.is-overflow {
  opacity: 0.72;
}

.co-disk-warn {
  color: var(--co-warning-text);
  font-weight: 500;
}

.co-disk-danger {
  color: var(--co-danger, #cf1322);
  font-weight: 500;
}

.co-model-row-skeleton {
  background: var(--co-skeleton-card-bg);
  pointer-events: none;
}

.co-model-row-skeleton:hover {
  border-color: var(--co-border-secondary);
  box-shadow: none;
}

.co-model-sk-actions {
  gap: 6px;
}

.co-model-strip-skeleton {
  background: var(--co-skeleton-card-bg);
}

.co-model-row-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 0;
}

.co-model-info {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
  flex: 1;
}

.co-model-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.co-model-name {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-model-fit-pill {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 1.4;
  padding: 0 6px;
}

.co-model-sub {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-muted);
  overflow-wrap: anywhere;
}

.co-model-desc {
  color: var(--co-secondary-text);
  overflow-wrap: anywhere;
}

.co-model-actions {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.co-icon-btn {
  color: var(--co-muted);
}

.co-icon-btn:hover,
.co-icon-btn.active {
  color: var(--co-accent);
}

.co-model-warning {
  padding: 6px 10px;
  border-radius: 6px;
}

.co-inline-progress {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-top: 2px;
}

.co-inline-progress-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--co-secondary-text);
}

/* Inline Repo Version Picker */
.co-repo-picker {
  padding-top: 10px;
  border-top: 1px solid var(--co-border-secondary);
}

/* Optional Expandable Parameters */
.co-config-drawer {
  padding: 12px;
  border-radius: 10px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-text);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-config-actions {
  display: flex;
  justify-content: flex-end;
}

/* Empty State */
.co-empty-panel {
  padding: 28px 16px;
  border-radius: 8px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

/* Delete Modal */
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

.co-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 4px;
}

@media (max-width: 560px) {
  .co-model-row-main {
    flex-wrap: wrap;
  }

  .co-model-actions {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
