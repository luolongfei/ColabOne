<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Badge as ABadge,
  Button as AButton,
  Drawer as ADrawer,
  Empty as AEmpty,
  Input as AInput,
  InputNumber as AInputNumber,
  Modal as AModal,
  Progress as AProgress,
  Select as ASelect,
  SelectOption as ASelectOption,
  Slider as ASlider,
  Space as ASpace,
  Spin as ASpin,
  TabPane as ATabPane,
  Tabs as ATabs,
  Tag as ATag,
  Tooltip as ATooltip,
} from "ant-design-vue";
import {
  PhArrowRight,
  PhArrowSquareOut,
  PhCaretDown,
  PhCaretRight,
  PhCheck,
  PhCube,
  PhDownloadSimple,
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
import { colabOneCommand, colabOneOpenURL } from "./api";
import {
  describeModelRemoteStatus,
  filterCommonModels,
  type ModelLibraryTab,
} from "./modelLibrary";
import {
  canColabOneLoadModel,
  type CommonRepo,
  type ModelSpec,
  type ColabOneSnapshot,
} from "./types";

const props = defineProps<{
  open: boolean;
  state: ColabOneSnapshot;
  busy?: boolean;
}>();

const emit = defineEmits<{
  "update:open": [boolean];
  device: [];
  sync: [];
  notify: [string];
  error: [string];
}>();

type RepoFile = {
  filename: string;
  size: number;
  shards: number;
  quantization: string;
  supported: boolean;
  reason: string;
};

const localBusy = ref(false);
const isBusy = computed(() => localBusy.value || !!props.busy);

const viewportWidth = ref(
  typeof window !== "undefined" ? window.innerWidth : 1280,
);
const drawerWidth = computed(() =>
  Math.min(680, Math.max(360, viewportWidth.value - 48)),
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

// Inline version picker for saved repositories in "我的"
const expandedRepo = ref("");
const repoFiles = ref<RepoFile[]>([]);
const repoRevision = ref("");
const repoSelectedFile = ref("");
const repoFilesLoading = ref(false);
const repoFilesError = ref("");
const repoInstalling = ref(false);
const repoAdvancedOpen = ref(false);
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
      resetRepoPicker();
    }
  },
);

function resetRepoPicker() {
  ++repoFileGen;
  expandedRepo.value = "";
  repoFiles.value = [];
  repoRevision.value = "";
  repoSelectedFile.value = "";
  repoFilesLoading.value = false;
  repoFilesError.value = "";
  repoAdvancedOpen.value = false;
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

const selectedRepoFileObj = computed(() =>
  repoFiles.value.find((f) => f.filename === repoSelectedFile.value),
);

const contextLengthOptions = [
  { label: "8K", value: 8192 },
  { label: "16K", value: 16384 },
  { label: "32K", value: 32768 },
  { label: "64K", value: 65536 },
];

const bytes = (n = 0) =>
  n >= 1024 ** 3
    ? (n / 1024 ** 3).toFixed(1) + " GB"
    : (n / 1024 ** 2).toFixed(0) + " MB";

const fileSize = (n: number) =>
  n < 1024 ** 3
    ? (n / 1024 ** 2).toFixed(0) + " MiB"
    : (n / 1024 ** 3).toFixed(2) + " GiB";

const duration = (seconds: number | null) => {
  if (seconds === null) return "计算中";
  const whole = Math.ceil(Math.max(0, seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
};

function customRepoNote(note?: string): string {
  const trimmed = (note || "").trim();
  if (!trimmed || trimmed === "默认常用模型" || trimmed === "默认模型") {
    return "";
  }
  return trimmed;
}

function conciseStatusBadge(model: ModelSpec): {
  label: string;
  color: "success" | "processing" | "warning" | "blue";
  dot?: boolean;
} | null {
  const status = describeModelRemoteStatus(model, props.state.runtime);
  if (status.state === "loaded") {
    return { label: "使用中", color: "success", dot: true };
  }
  if (status.state === "loading") {
    return { label: "装载中…", color: "warning" };
  }
  if (status.state === "downloading") {
    const pct = Math.round((status.progress || 0) * 100);
    return { label: `下载中 ${pct}%`, color: "processing" };
  }
  if (status.state === "downloaded") {
    return { label: "已下载", color: "blue" };
  }
  return null;
}

function vramWarning(model: ModelSpec): string {
  const gpu = props.state.runtime.gpu;
  if (
    props.state.runtime.state === "stopped" ||
    !gpu?.total_mib ||
    !model.min_vram_mib
  ) {
    return "";
  }
  if (gpu.total_mib < model.min_vram_mib) {
    const haveGb = (gpu.total_mib / 1024).toFixed(1).replace(/\.0$/, "");
    const needGb = (model.min_vram_mib / 1024).toFixed(1).replace(/\.0$/, "");
    return `当前显存 ${haveGb} GB 低于建议值 ${needGb} GB，装载可能失败或较慢`;
  }
  return "";
}

function diskWarning(model: ModelSpec): string {
  const free = props.state.runtime.disk_free;
  if (
    props.state.runtime.state === "stopped" ||
    model.downloaded ||
    !free ||
    !model.size
  ) {
    return "";
  }
  if (free < model.size) {
    return `远端可用磁盘 ${bytes(free)} 小于模型体积 ${bytes(model.size)}`;
  }
  return "";
}

const downloadDisabledReason = computed(() => {
  if (!ready.value) return "请先启动远端 GPU";
  if (props.state.operation) return props.state.operation.title;
  if (task.value) return "请等待当前任务完成";
  if (!props.state.runtime.protocol) return "正在同步远端状态…";
  return "";
});

const loadDisabledReason = computed(() => {
  if (!ready.value) return "请先启动远端 GPU";
  if (props.state.operation) return props.state.operation.title;
  if (task.value) return "请等待当前任务完成";
  return "";
});

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
  repoSelectedFile.value = "";
  repoFilesError.value = "";
  repoAdvancedOpen.value = false;
  repoFilesLoading.value = true;
  try {
    const result = await colabOneCommand("hub.files", { repo });
    if (gen !== repoFileGen) return;
    repoRevision.value = result.revision || "";
    repoFiles.value = result.files || [];
    repoSelectedFile.value =
      repoFiles.value.find(
        (f) => f.supported && /Q4_K_M/i.test(f.quantization),
      )?.filename ||
      repoFiles.value.find((f) => f.supported)?.filename ||
      "";
  } catch (e) {
    if (gen === repoFileGen) repoFilesError.value = String(e);
  } finally {
    if (gen === repoFileGen) repoFilesLoading.value = false;
  }
}

function onRepoFileChange(raw: unknown) {
  repoSelectedFile.value = String(raw || "");
}

function onRepoContextChange(raw: unknown) {
  const num = Number(raw);
  if (Number.isFinite(num) && num > 0) repoContextLength.value = num;
}

function onRepoTempChange(raw: string | number | null) {
  if (raw === null || raw === "") return;
  const num = Number(raw);
  if (Number.isFinite(num)) {
    repoTemperature.value = Math.min(2, Math.max(0, Math.round(num * 10) / 10));
  }
}

async function installFromMyRepo(mode: "load" | "download" | "save") {
  if (!expandedRepo.value || !repoSelectedFile.value || repoInstalling.value) {
    return;
  }
  if (mode !== "save" && (!ready.value || !!task.value || isBusy.value)) {
    return;
  }
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
    resetRepoPicker();
    emit("sync");
    emit(
      "notify",
      mode === "load"
        ? "正在下载并装载模型，可在底部查看进度"
        : mode === "download"
          ? "正在下载到远端，可在底部查看进度"
          : "已保存此版本到「我的」",
    );
  } catch (e) {
    repoFilesError.value = String(e);
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
        ? `已将 ${model.name} 加入「我的」`
        : `已将 ${model.name} 移出「我的」`,
    );
  }
}

async function removeMyRepo(repo: CommonRepo) {
  if (expandedRepo.value === repo.repo) resetRepoPicker();
  const res = await act("common_repo.remove", { repo: repo.repo });
  if (res) {
    emit("notify", `已将 ${repo.repo} 移出「我的」`);
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
        ? "正在下载并装载模型，可在底部查看进度"
        : "正在下载模型，可在底部查看进度",
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
    emit("notify", "正在装载模型到显存");
  }
}

async function unloadModel() {
  await act("model.unload");
}

function confirmDeleteModel() {
  if (!deleteTarget.value) return;
  const targetId = deleteTarget.value.id;
  deleteTarget.value = null;
  void act("model.delete", { id: targetId });
  emit("notify", "正在删除远端模型文件");
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
      ? "已加入我的模型"
      : mode === "load"
        ? "正在下载并装载模型，可在底部查看进度"
        : "正在下载到远端，可在底部查看进度",
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
        <span>选择模型</span>
      </div>
    </template>

    <div
      class="co-model-drawer-body co-models-drawer"
      role="region"
      aria-label="选择 API 模型"
    >
      <!-- Slim Active Status Strip (only when loaded or subtle hint when offline) -->
      <div v-if="activeVramModel" class="co-active-bar co-vram-console">
        <div class="co-active-bar-info">
          <ABadge status="processing" color="#52c41a" />
          <strong class="co-active-bar-name">
            当前使用：{{ activeVramModel.name }}
          </strong>
          <span class="co-active-bar-spec co-tabular">
            {{ activeContextK }}K · 温度 {{ activeTemp }}
          </span>
        </div>
        <ASpace :size="6">
          <AButton type="primary" size="small" @click="closeDrawer">
            <template #icon><PhCheck :size="13" /></template>
            使用此模型
          </AButton>
          <AButton
            size="small"
            :disabled="!!task || isBusy"
            @click="unloadModel"
          >
            卸载
          </AButton>
        </ASpace>
      </div>

      <div v-else-if="!ready" class="co-subtle-hint co-gpu-hint-bar">
        <span>
          {{
            gpuStarting
              ? "远端 GPU 正在启动中，就绪后即可下载并装载模型。"
              : "远端 GPU 未启动，可先浏览或将模型加入「我的」。"
          }}
        </span>
        <AButton
          type="link"
          size="small"
          class="co-inline-link co-gpu-hint-link"
          @click="goDevice"
        >
          {{ gpuStarting ? "查看 GPU 启动进度" : "去启动 GPU" }}
          <PhArrowRight :size="13" />
        </AButton>
      </div>

      <div
        v-else-if="state.runtime.comfyui?.running"
        class="co-subtle-hint"
      >
        装载模型时将自动停止 ComfyUI 以释放显存。
      </div>

      <!-- Two Primary Entries: 我的 & 社区 -->
      <div class="co-model-tabs-bar co-model-tabs">
        <ATabs
          :active-key="modelTab"
          class="co-tabs-inline"
          @change="onModelTabChange"
        >
          <ATabPane key="common">
            <template #tab>
              <span class="co-tab-label">
                <span>我的</span>
                <span v-if="myTotal" class="co-tab-count co-tabular">
                  {{ myTotal }}
                </span>
              </span>
            </template>
          </ATabPane>
          <ATabPane key="hub" tab="社区" />
        </ATabs>
      </div>

      <!-- Entry 2: 社区 -->
      <HubBrowser
        v-if="modelTab === 'hub'"
        :ready="ready"
        :disabled="
          !!task || isBusy || !!state.operation || !state.runtime.protocol
        "
        :common-repos="state.common_repos"
        :initial-query="hubTargetQuery"
        :models="state.models"
        :runtime="state.runtime"
        @common-repo-updated="onHubRepoUpdated"
        @installed="onHubInstalled"
        @toggle-common="toggleModelMine"
        @use-loaded="closeDrawer"
      />

      <!-- Entry 1: 我的 -->
      <template v-else>
        <div
          v-if="myTotal > 0"
          class="co-list-toolbar co-list-toolbar"
        >
          <AInput
            v-model:value="search"
            type="search"
            allow-clear
            class="co-model-search"
            placeholder="搜索我的模型…"
            aria-label="查找模型"
          >
            <template #prefix>
              <PhMagnifyingGlass :size="15" class="co-input-prefix" />
            </template>
          </AInput>
        </div>

        <!-- My Models + My Saved Repos Unified Clean List -->
        <div
          v-if="filtered.length || filteredMyRepos.length"
          class="co-model-list co-model-list"
        >
          <div
            v-for="model in filtered"
            :key="model.id"
            class="co-model-row co-model-card"
            :class="{
              loaded: isModelLoaded(model),
              downloading: modelRunningTask(model)?.kind === 'download',
              loading: modelRunningTask(model)?.kind === 'load',
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
                </div>

                <div class="co-model-sub co-tabular">
                  <span>{{ bytes(model.size) }}</span>
                  <template v-if="model.min_vram_mib">
                    <span>·</span>
                    <span>建议 {{ Math.ceil(model.min_vram_mib / 1024) }} GB 显存</span>
                  </template>
                  <template v-if="model.description">
                    <span>·</span>
                    <span class="co-model-desc">{{ model.description }}</span>
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
                    使用中
                  </AButton>
                  <AButton
                    size="small"
                    :disabled="!!task || isBusy"
                    @click="unloadModel"
                  >
                    卸载
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
                    取消下载
                  </AButton>
                </template>

                <template v-else-if="model.downloaded">
                  <ATooltip :title="loadDisabledReason">
                    <AButton
                      type="primary"
                      size="small"
                      :disabled="!canColabOneLoadModel(model, state) || isBusy"
                      @click="loadModel(model)"
                    >
                      <template #icon><PhPlay :size="13" weight="fill" /></template>
                      装载
                    </AButton>
                  </ATooltip>
                </template>

                <template v-else>
                  <ATooltip :title="downloadDisabledReason">
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
                      下载并装载
                    </AButton>
                  </ATooltip>
                </template>

                <!-- Quiet Secondary Icon Controls -->
                <ATooltip
                  v-if="modelRunningTask(model)?.kind !== 'download'"
                  title="上下文与温度设置"
                >
                  <AButton
                    type="text"
                    size="small"
                    class="co-icon-btn"
                    :class="{ active: configModelId === model.id }"
                    aria-label="参数"
                    :aria-expanded="configModelId === model.id"
                    @click="toggleConfig(model)"
                  >
                    <template #icon>
                      <PhSlidersHorizontal :size="15" />
                    </template>
                  </AButton>
                </ATooltip>

                <ATooltip :title="model.common ? '移出我的' : '加入我的'">
                  <AButton
                    type="text"
                    size="small"
                    class="co-icon-btn"
                    :aria-label="
                      model.common
                        ? `移出我的 ${model.name}`
                        : `加入我的 ${model.name}`
                    "
                    :disabled="isBusy"
                    @click="toggleModelMine(model)"
                  >
                    <template #icon>
                      <PhPushPinSlash v-if="model.common" :size="15" />
                      <PhPushPin v-else :size="15" />
                    </template>
                  </AButton>
                </ATooltip>

                <ATooltip
                  v-if="model.downloaded && !isModelLoaded(model)"
                  title="删除远端模型文件"
                >
                  <AButton
                    type="text"
                    danger
                    size="small"
                    class="co-icon-btn"
                    aria-label="删除远端模型文件"
                    :disabled="!!task || isBusy"
                    @click="deleteTarget = model"
                  >
                    <template #icon>
                      <PhTrash :size="15" />
                    </template>
                  </AButton>
                </ATooltip>

                <ATooltip
                  v-else-if="model.custom && !model.downloaded"
                  title="移除本地记录"
                >
                  <AButton
                    type="text"
                    danger
                    size="small"
                    class="co-icon-btn"
                    aria-label="移除本地模型记录"
                    :disabled="isBusy"
                    @click="act('model.remove_local', { id: model.id })"
                  >
                    <template #icon>
                      <PhTrash :size="15" />
                    </template>
                  </AButton>
                </ATooltip>
              </div>
            </div>

            <AAlert
              v-if="vramWarning(model) || diskWarning(model)"
              type="warning"
              show-icon
              :message="vramWarning(model) || diskWarning(model)"
              class="co-model-warning co-model-vram-warning"
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
                      ? "正在校验完整性…"
                      : `正在下载 · ${Math.round((modelRunningTask(model)!.progress || 0) * 100)}%`
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
                    · 剩余
                    {{
                      duration(modelRunningTask(model)!.download!.eta_seconds)
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

            <!-- Optional Expandable Parameters (collapsed by default) -->
            <div
              v-if="configModelId === model.id"
              class="co-config-drawer co-model-config-drawer"
            >
              <div class="co-config-row">
                <span class="co-field-label">上下文长度</span>
                <ASpace :size="6" wrap>
                  <AButton
                    v-for="opt in contextLengthOptions"
                    :key="opt.value"
                    size="small"
                    :type="
                      optsFor(model).context_length === opt.value
                        ? 'primary'
                        : 'default'
                    "
                    :aria-pressed="optsFor(model).context_length === opt.value"
                    @click="setModelContextLength(model, opt.value)"
                  >
                    {{ opt.label }}
                  </AButton>
                </ASpace>
              </div>

              <div class="co-config-slider">
                <div class="co-slider-header">
                  <span class="co-field-label">温度（Temperature）</span>
                  <strong class="co-tabular">{{
                    optsFor(model).temperature.toFixed(1)
                  }}</strong>
                </div>
                <ASlider
                  :value="optsFor(model).temperature"
                  :min="0"
                  :max="2"
                  :step="0.1"
                  @update:value="(v) => setModelTemperature(model, v)"
                />
              </div>

              <div class="co-config-actions">
                <AButton
                  v-if="isModelLoaded(model)"
                  type="primary"
                  size="small"
                  :disabled="!!task || isBusy || !!state.operation"
                  @click="loadModel(model)"
                >
                  应用并重新装载
                </AButton>
                <AButton
                  v-else-if="model.downloaded"
                  type="primary"
                  size="small"
                  :disabled="!canColabOneLoadModel(model, state) || isBusy"
                  @click="loadModel(model)"
                >
                  <template #icon><PhPlay :size="13" weight="fill" /></template>
                  装载显存
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
                  按此参数下载并装载
                </AButton>
              </div>
            </div>
          </div>

          <!-- My Saved Community Repositories (inline 1-click version selection) -->
          <div
            v-for="bm in filteredMyRepos"
            :key="bm.repo"
            class="co-model-row co-common-repo-row"
            :class="{ selected: expandedRepo === bm.repo }"
          >
            <div class="co-model-row-main">
              <div class="co-model-info">
                <div class="co-model-title-row">
                  <h3 class="co-model-name">{{ bm.name }}</h3>
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
                  {{ expandedRepo === bm.repo ? "收起" : "选择版本" }}
                  <PhCaretDown v-if="expandedRepo === bm.repo" :size="12" />
                  <PhCaretRight v-else :size="12" />
                </AButton>
                <ATooltip title="移出我的">
                  <AButton
                    type="text"
                    size="small"
                    class="co-icon-btn"
                    :aria-label="`移出我的 ${bm.repo}`"
                    :disabled="isBusy"
                    @click="removeMyRepo(bm)"
                  >
                    <template #icon>
                      <PhPushPinSlash :size="15" />
                    </template>
                  </AButton>
                </ATooltip>
              </div>
            </div>

            <!-- Inline Foolproof Version Picker right inside "我的" -->
            <div
              v-if="expandedRepo === bm.repo"
              class="co-repo-picker"
              aria-label="选择模型版本"
            >
              <div v-if="repoFilesLoading" class="co-picker-loading" role="status">
                <ASpin size="small" tip="正在读取可选版本…" />
              </div>

              <AAlert
                v-else-if="repoFilesError"
                type="error"
                show-icon
                :message="repoFilesError"
                role="alert"
              >
                <template #action>
                  <AButton
                    size="small"
                    @click="toggleRepoVersions(bm.repo, true)"
                  >
                    重试
                  </AButton>
                </template>
              </AAlert>

              <template v-else-if="repoFiles.length">
                <ASelect
                  :value="repoSelectedFile || undefined"
                  aria-label="选择量化版本"
                  placeholder="选择量化版本"
                  class="co-full-select"
                  @change="onRepoFileChange"
                >
                  <ASelectOption
                    v-for="file in repoFiles"
                    :key="file.filename"
                    :value="file.filename"
                    :disabled="!file.supported"
                  >
                    {{ file.filename }} · {{ fileSize(file.size) }} ·
                    {{
                      file.shards > 1 ? `${file.shards} 个分片文件` : "1 个文件"
                    }}{{ file.reason ? " · " + file.reason : "" }}
                  </ASelectOption>
                </ASelect>

                <div
                  v-if="selectedRepoFileObj"
                  class="co-repo-picker-meta"
                >
                  <div class="co-repo-meta-left">
                    <ATag
                      v-if="selectedRepoFileObj.quantization"
                      color="blue"
                      :bordered="false"
                    >
                      {{ selectedRepoFileObj.quantization }}
                    </ATag>
                    <strong class="co-tabular">{{
                      fileSize(selectedRepoFileObj.size)
                    }}</strong>
                    <span>·</span>
                    <span>
                      {{
                        selectedRepoFileObj.shards > 1
                          ? `共 ${selectedRepoFileObj.shards} 个文件`
                          : "共 1 个文件"
                      }}
                    </span>
                    <AButton
                      type="link"
                      size="small"
                      class="co-inline-link"
                      @click="colabOneOpenURL('https://huggingface.co/' + bm.repo)"
                    >
                      仓库主页
                      <PhArrowSquareOut :size="12" />
                    </AButton>
                  </div>
                  <AButton
                    type="link"
                    size="small"
                    class="co-inline-link co-adv-toggle"
                    @click="repoAdvancedOpen = !repoAdvancedOpen"
                  >
                    <PhSlidersHorizontal :size="13" />
                    <span>{{ repoAdvancedOpen ? "收起参数" : "参数" }}</span>
                  </AButton>
                </div>

                <div v-if="repoAdvancedOpen" class="co-repo-adv-row">
                  <div class="co-adv-field">
                    <span class="co-field-label">上下文</span>
                    <ASelect
                      :value="repoContextLength"
                      size="small"
                      aria-label="上下文长度"
                      class="co-adv-ctx"
                      @change="onRepoContextChange"
                    >
                      <ASelectOption
                        v-for="opt in contextLengthOptions"
                        :key="opt.value"
                        :value="opt.value"
                      >
                        {{ opt.label }}
                      </ASelectOption>
                    </ASelect>
                  </div>
                  <div class="co-adv-field">
                    <span class="co-field-label">温度</span>
                    <AInputNumber
                      :value="repoTemperature"
                      :min="0"
                      :max="2"
                      :step="0.1"
                      size="small"
                      aria-label="温度参数"
                      class="co-adv-temp"
                      @update:value="onRepoTempChange"
                    />
                  </div>
                </div>

                <ASpace wrap :size="8">
                  <ATooltip :title="downloadDisabledReason">
                    <AButton
                      type="primary"
                      size="small"
                      :loading="repoInstalling"
                      :disabled="
                        !ready ||
                        !!task ||
                        isBusy ||
                        repoInstalling ||
                        !!state.operation ||
                        !state.runtime.protocol ||
                        !repoSelectedFile
                      "
                      @click="installFromMyRepo('load')"
                    >
                      <template #icon><PhPlay :size="13" weight="fill" /></template>
                      {{ repoInstalling ? "正在提交…" : "下载并装载" }}
                    </AButton>
                  </ATooltip>
                  <ATooltip :title="downloadDisabledReason">
                    <AButton
                      v-if="ready"
                      size="small"
                      :disabled="
                        !!task ||
                        isBusy ||
                        repoInstalling ||
                        !!state.operation ||
                        !state.runtime.protocol ||
                        !repoSelectedFile
                      "
                      @click="installFromMyRepo('download')"
                    >
                      <template #icon><PhDownloadSimple :size="14" /></template>
                      仅下载
                    </AButton>
                  </ATooltip>
                </ASpace>
              </template>

              <AEmpty
                v-else
                description="该仓库暂无可用的 GGUF 模型文件"
                class="co-picker-empty"
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
            <AEmpty :description="`没有找到与「${search.trim()}」匹配的模型`">
              <ASpace :size="8" wrap>
                <AButton size="small" @click="search = ''">清空搜索</AButton>
                <AButton
                  type="primary"
                  size="small"
                  @click="searchInCommunity(search.trim())"
                >
                  去社区搜索「{{ search.trim() }}」
                </AButton>
              </ASpace>
            </AEmpty>
          </template>
          <template v-else>
            <AEmpty description="「我的」中还没有模型">
              <AButton
                type="primary"
                size="small"
                @click="onModelTabChange('hub')"
              >
                <template #icon><PhGlobe :size="14" /></template>
                去社区发现模型
              </AButton>
            </AEmpty>
          </template>
        </div>
      </template>
    </div>

    <!-- Delete Remote Model File Confirmation -->
    <AModal
      :open="!!deleteTarget"
      title="删除远端模型文件？"
      :width="420"
      :z-index="1050"
      :footer="null"
      centered
      @cancel="deleteTarget = null"
    >
      <div class="co-modal-stack">
        <p v-if="deleteTarget" class="co-muted co-modal-lead">
          仅删除远端磁盘上的 {{ deleteTarget.name }} 模型文件，「我的」列表记录会保留。
        </p>
        <div class="co-modal-footer">
          <AButton @click="deleteTarget = null">取消</AButton>
          <AButton type="primary" danger @click="confirmDeleteModel">
            删除文件
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

/* 2-Entry Tabs */
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
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
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
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-picker-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 12px 8px;
}

.co-full-select {
  width: 100%;
}

.co-repo-picker-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-repo-meta-left {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.co-repo-meta-left strong {
  color: var(--co-text);
  font-weight: 600;
}

.co-adv-toggle {
  font-size: 12px !important;
  color: var(--co-muted);
}

.co-repo-adv-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  padding: 8px 10px;
  border-radius: 6px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-adv-field {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-adv-ctx {
  width: 96px;
}

.co-adv-temp {
  width: 78px;
}

.co-picker-empty {
  margin: 6px 0;
}

/* Optional Expandable Parameters */
.co-config-drawer {
  padding: 10px 12px;
  border-radius: 6px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-text);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-config-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}

.co-field-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--co-secondary-text);
}

.co-config-slider {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-slider-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
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
