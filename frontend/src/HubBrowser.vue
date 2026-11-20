<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Button as AButton,
  Empty as AEmpty,
  Input as AInput,
  InputNumber as AInputNumber,
  Select as ASelect,
  SelectOption as ASelectOption,
  Space as ASpace,
  Spin as ASpin,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhArrowSquareOut,
  PhCaretDown,
  PhCaretRight,
  PhCheck,
  PhDownloadSimple,
  PhHeart,
  PhLock,
  PhMagnifyingGlass,
  PhPlay,
  PhPushPin,
  PhPushPinSlash,
  PhSlidersHorizontal,
} from "@phosphor-icons/vue";
import { colabOneCommand, colabOneOpenURL } from "./api";
import type { CommonRepo, ModelSpec, RuntimeStatus } from "./types";
import { repoRemoteSummary } from "./modelLibrary";

const props = defineProps<{
  ready: boolean;
  disabled: boolean;
  commonRepos?: CommonRepo[];
  initialQuery?: string;
  initialRepo?: string;
  models?: ModelSpec[];
  runtime?: RuntimeStatus;
}>();

const emit = defineEmits<{
  installed: [mode: "save" | "download" | "load"];
  commonRepoUpdated: [message: string];
  toggleCommon: [model: ModelSpec];
  useLoaded: [];
}>();

type Repo = {
  repo: string;
  downloads: number;
  likes: number;
  gated: boolean;
  minSize?: number;
  maxSize?: number;
  versions?: number;
  sizeState?: "loading" | "ready" | "unavailable";
};
type Detail = { revision: string; files: HubFile[] };
type HubFile = {
  filename: string;
  size: number;
  shards: number;
  quantization: string;
  supported: boolean;
  reason: string;
};

const CONTEXT_OPTIONS = [
  { value: 4096, label: "4K" },
  { value: 8192, label: "8K（默认）" },
  { value: 16384, label: "16K" },
  { value: 32768, label: "32K" },
  { value: 65536, label: "64K" },
];

const REPO_PATTERN = /^[\w.-]+\/[\w.-]+$/;
const HF_URL_PATTERN =
  /^https?:\/\/(?:www\.)?huggingface\.co\/([\w.-]+\/[\w.-]+)(?:\/(?:blob|resolve|tree)\/([^/?#]+)(?:\/([^?#]+))?)?\/?(?:\?[^#]*)?(?:#.*)?$/i;

const selection = ref<HTMLElement | HTMLElement[] | null>(null);
const query = ref("");
const sort = ref("downloads");
const repos = ref<Repo[]>([]);
const cursor = ref("");
const loading = ref(false);
const error = ref("");
const chosen = ref("");
const customName = ref("");
const revision = ref("");
const files = ref<HubFile[]>([]);
const filename = ref("");
const filesLoading = ref(false);
const fileError = ref("");
const installing = ref(false);
const togglingRepo = ref("");
const showAdvanced = ref(false);
const contextLength = ref<number>(8192);
const temperature = ref<number>(0.7);

const effectiveRuntime = computed<RuntimeStatus>(
  () => props.runtime || { state: props.ready ? "ready" : "stopped" },
);

function repoAuthor(repo: string): string {
  const idx = repo.indexOf("/");
  return idx > 0 ? repo.slice(0, idx) : "";
}

function repoShortName(repo: string): string {
  const idx = repo.indexOf("/");
  return idx > 0 ? repo.slice(idx + 1) : repo;
}

const commonRepoSet = computed(
  () => new Set((props.commonRepos || []).map((b) => b.repo.toLowerCase())),
);
const isCommonRepo = (repo: string) =>
  commonRepoSet.value.has(repo.toLowerCase());

const summaryByRepo = computed(() => {
  const models = props.models || [];
  const rt = effectiveRuntime.value;
  const map = new Map<string, ReturnType<typeof repoRemoteSummary>>();
  for (const r of repos.value) {
    map.set(r.repo, repoRemoteSummary(r.repo, models, rt));
  }
  if (chosen.value && !map.has(chosen.value)) {
    map.set(chosen.value, repoRemoteSummary(chosen.value, models, rt));
  }
  return map;
});

function summaryFor(repoName: string) {
  return (
    summaryByRepo.value.get(repoName) ||
    repoRemoteSummary(repoName, props.models || [], effectiveRuntime.value)
  );
}

function loadedModelFor(repoName: string): ModelSpec | undefined {
  const rt = effectiveRuntime.value;
  if (rt.state === "stopped") return undefined;
  return summaryFor(repoName).savedModels.find(
    (m) => m.loaded || (!!m.id && rt.loaded_model === m.id),
  );
}

const isRepoMarked = (repoName: string) =>
  isCommonRepo(repoName) || summaryFor(repoName).common;

const chosenSummary = computed(() =>
  chosen.value ? summaryFor(chosen.value) : null,
);

function matchedSavedModel(fileName: string): ModelSpec | undefined {
  if (!fileName || !chosenSummary.value) return undefined;
  const matches = chosenSummary.value.savedModels.filter((m) =>
    m.files?.some((f) => f.path === fileName),
  );
  if (!matches.length) return undefined;
  return (
    matches.find(
      (m) => m.loaded || effectiveRuntime.value.loaded_model === m.id,
    ) ||
    matches.find((m) => m.downloaded) ||
    matches.find((m) => m.common) ||
    matches[0]
  );
}

function fileSavedTag(fileName: string): string {
  const saved = matchedSavedModel(fileName);
  if (!saved) return "";
  if (
    saved.downloaded ||
    (effectiveRuntime.value.state !== "stopped" &&
      (saved.loaded || effectiveRuntime.value.loaded_model === saved.id))
  ) {
    return "已下载";
  }
  if (saved.common) {
    return "已在我的";
  }
  return "";
}

const selectedFile = computed(() =>
  files.value.find((file) => file.filename === filename.value),
);
const selectedSavedTag = computed(() =>
  selectedFile.value ? fileSavedTag(selectedFile.value.filename) : "",
);

let generation = 0;
let fileGeneration = 0;
let timer: ReturnType<typeof setTimeout>;
const size = (n: number) =>
  n < 1024 ** 3
    ? (n / 1024 ** 2).toFixed(0) + " MiB"
    : (n / 1024 ** 3).toFixed(2) + " GiB";
const details = new Map<string, Detail>();
const pendingDetails = new Map<string, Promise<Detail>>();

async function detail(repo: string): Promise<Detail> {
  const cached = details.get(repo);
  if (cached) return cached;
  const pending = pendingDetails.get(repo);
  if (pending) return pending;
  const request: Promise<Detail> = colabOneCommand("hub.files", { repo })
    .then((result) => {
      if (details.size >= 80) details.delete(details.keys().next().value!);
      details.set(repo, result);
      return result;
    })
    .finally(() => pendingDetails.delete(repo));
  pendingDetails.set(repo, request);
  return request;
}

async function previewSize(repo: string, id: number) {
  try {
    const result = await detail(repo);
    if (id !== generation) return;
    const row = repos.value.find((item) => item.repo === repo);
    if (!row) return;
    const sizes = result.files
      .filter((f) => f.supported && f.size > 0)
      .map((f) => f.size);
    Object.assign(
      row,
      sizes.length
        ? {
            minSize: Math.min(...sizes),
            maxSize: Math.max(...sizes),
            versions: sizes.length,
            sizeState: "ready",
          }
        : { sizeState: "unavailable" },
    );
  } catch {
    if (id === generation) {
      const row = repos.value.find((item) => item.repo === repo);
      if (row) row.sizeState = "unavailable";
    }
  }
}

async function previewSizes(batch: Repo[], id: number) {
  let index = 0;
  async function worker() {
    while (id === generation && index < batch.length) {
      const repo = batch[index++]!.repo;
      await previewSize(repo, id);
    }
  }
  await Promise.all([worker(), worker(), worker()]);
}

async function search(more = false) {
  const id = ++generation;
  loading.value = true;
  error.value = "";
  if (!more) {
    repos.value = [];
    cursor.value = "";
  }
  try {
    const result = await colabOneCommand("hub.search", {
      query: query.value,
      sort: sort.value,
      cursor: more ? cursor.value : "",
    });
    if (id !== generation) return;
    result.models = result.models.map((r: Repo) => ({
      ...r,
      sizeState: "loading",
    }));
    const all = more ? [...repos.value, ...result.models] : result.models;
    repos.value = [
      ...new Map<string, Repo>(all.map((r: Repo) => [r.repo, r])).values(),
    ];
    cursor.value = result.next_cursor;
    void previewSizes(
      repos.value.filter((repo) => repo.sizeState === "loading"),
      id,
    );
  } catch (e) {
    if (id === generation) error.value = String(e);
  } finally {
    if (id === generation) loading.value = false;
  }
}

function submitSearch() {
  clearTimeout(timer);
  const trimmed = query.value.trim();
  const urlMatch = trimmed.match(HF_URL_PATTERN);
  if (urlMatch && urlMatch[1]) {
    const targetRepo = urlMatch[1];
    const targetFile =
      urlMatch[3] && urlMatch[3].toLowerCase().endsWith(".gguf")
        ? urlMatch[3]
        : "";
    suppressQueryWatch = true;
    query.value = targetRepo;
    void search(false).then(() => choose(targetRepo, true, targetFile));
    return;
  }
  if (REPO_PATTERN.test(trimmed)) {
    void search(false).then(() => choose(trimmed, true));
    return;
  }
  void search(false);
}

function onSortChange(val: unknown) {
  sort.value = String(val || "downloads");
}

function onFilenameChange(val: unknown) {
  filename.value = String(val || "");
}

function onContextLengthChange(val: unknown) {
  const next = Number(val);
  if (Number.isFinite(next) && next > 0) {
    contextLength.value = next;
  }
}

function onTemperatureUpdate(val: string | number | null) {
  if (val === null || val === "") return;
  const num = Number(val);
  if (Number.isFinite(num)) {
    temperature.value = Math.min(2, Math.max(0, Math.round(num * 10) / 10));
  }
}

let suppressQueryWatch = false;
let initialSearchDone = false;

watch(
  [query, sort],
  ([nextQuery]) => {
    if (suppressQueryWatch) {
      suppressQueryWatch = false;
      initialSearchDone = true;
      return;
    }
    const trimmed = String(nextQuery || "").trim();
    const urlMatch = trimmed.match(HF_URL_PATTERN);
    if (urlMatch && urlMatch[1]) {
      clearTimeout(timer);
      initialSearchDone = true;
      const targetRepo = urlMatch[1];
      const targetFile =
        urlMatch[3] && urlMatch[3].toLowerCase().endsWith(".gguf")
          ? urlMatch[3]
          : "";
      suppressQueryWatch = true;
      query.value = targetRepo;
      void search(false).then(() => choose(targetRepo, true, targetFile));
      return;
    }
    ++generation;
    loading.value = true;
    error.value = "";
    cursor.value = "";
    repos.value = [];
    ++fileGeneration;
    chosen.value = "";
    files.value = [];
    filename.value = "";
    filesLoading.value = false;
    showAdvanced.value = false;
    clearTimeout(timer);
    if (!initialSearchDone) {
      initialSearchDone = true;
      void search();
      return;
    }
    timer = setTimeout(() => search(), 350);
  },
  { immediate: true },
);

watch(
  () => [props.initialRepo, props.initialQuery] as const,
  ([repo, q]) => {
    if (repo) {
      clearTimeout(timer);
      if (query.value !== repo) {
        suppressQueryWatch = true;
        query.value = repo;
      }
      void search().then(() => choose(repo, true));
      return;
    }
    if (q) {
      clearTimeout(timer);
      if (query.value !== q) {
        suppressQueryWatch = true;
        query.value = q;
      }
      void search(false);
    }
  },
  { immediate: true },
);

async function choose(repo: string, forceOpen = false, preferredFile = "") {
  if (!forceOpen && chosen.value === repo) {
    ++fileGeneration;
    chosen.value = "";
    files.value = [];
    filename.value = "";
    filesLoading.value = false;
    fileError.value = "";
    showAdvanced.value = false;
    return;
  }
  const id = ++fileGeneration;
  chosen.value = repo;
  customName.value = "";
  files.value = [];
  filename.value = "";
  revision.value = "";
  fileError.value = "";
  showAdvanced.value = false;
  filesLoading.value = true;
  try {
    const result = await detail(repo);
    if (id !== fileGeneration) return;
    revision.value = result.revision;
    files.value = result.files;
    filename.value =
      (preferredFile &&
        result.files.find(
          (f: HubFile) => f.supported && f.filename === preferredFile,
        )?.filename) ||
      result.files.find(
        (f: HubFile) => f.supported && /Q4_K_M/i.test(f.quantization),
      )?.filename ||
      result.files.find((f: HubFile) => f.supported)?.filename ||
      "";
  } catch (e) {
    if (id === fileGeneration) fileError.value = String(e);
  } finally {
    if (id === fileGeneration) {
      filesLoading.value = false;
      await nextTick();
      const target = Array.isArray(selection.value)
        ? selection.value.find(Boolean)
        : selection.value;
      target?.scrollIntoView?.({ behavior: "smooth", block: "nearest" });
    }
  }
}

async function toggleCommonRepo(repo: Repo) {
  if (togglingRepo.value) return;
  const summary = summaryFor(repo.repo);
  const inCommonRepos = isCommonRepo(repo.repo);
  if (!inCommonRepos && summary.common) {
    for (const m of summary.savedModels) {
      if (m.common) emit("toggleCommon", m);
    }
    return;
  }
  togglingRepo.value = repo.repo;
  try {
    const res = await colabOneCommand("common_repo.save", {
      repo: repo.repo,
      name: repo.repo.split("/").pop() || repo.repo,
      downloads: repo.downloads,
      likes: repo.likes,
      toggle: true,
    });
    if (inCommonRepos && !res?.common && summary.common) {
      for (const m of summary.savedModels) {
        if (m.common) emit("toggleCommon", m);
      }
    }
    emit(
      "commonRepoUpdated",
      res?.common
        ? `已将 ${repo.repo} 加入「我的」`
        : `已将 ${repo.repo} 移出「我的」`,
    );
  } catch (e) {
    error.value = String(e);
  } finally {
    togglingRepo.value = "";
  }
}

async function install(mode: "save" | "download" | "load" = "load") {
  if (!filename.value || installing.value) return;
  if (mode !== "save" && (!props.ready || props.disabled)) return;
  installing.value = true;
  fileError.value = "";
  try {
    const payload: Record<string, unknown> = {
      repo: chosen.value,
      filename: filename.value,
      revision: revision.value,
      download: mode !== "save",
    };
    if (mode === "load") {
      payload.load = true;
      payload.context_length = contextLength.value;
      payload.temperature = temperature.value;
    }
    if (customName.value.trim()) payload.name = customName.value.trim();
    await colabOneCommand("hub.install", payload);
    emit("installed", mode);
  } catch (e) {
    fileError.value = String(e);
  } finally {
    installing.value = false;
  }
}

onUnmounted(() => {
  ++generation;
  ++fileGeneration;
  clearTimeout(timer);
});
</script>

<template>
  <div class="co-hub-browser">
    <div class="co-hub-search-bar">
      <AInput
        v-model:value="query"
        type="search"
        allow-clear
        aria-label="搜索 Hugging Face 模型"
        placeholder="搜索社区模型，或直接粘贴 Hugging Face 链接 / owner/repo"
        class="co-hub-search-input"
        @press-enter="submitSearch"
      >
        <template #prefix>
          <PhMagnifyingGlass :size="15" class="co-search-prefix-icon" />
        </template>
      </AInput>

      <ASelect
        :value="sort"
        aria-label="社区模型排序"
        class="co-hub-sort-select"
        @change="onSortChange"
      >
        <ASelectOption value="downloads">最多下载</ASelectOption>
        <ASelectOption value="likes">最多点赞</ASelectOption>
        <ASelectOption value="updated">最近更新</ASelectOption>
      </ASelect>
    </div>

    <AAlert
      v-if="error"
      type="error"
      show-icon
      :message="error"
      role="alert"
    >
      <template #action>
        <AButton size="small" @click="search()">重试</AButton>
      </template>
    </AAlert>

    <!-- Standalone selection when pasted/targeted repo is not in search list -->
    <div
      v-if="chosen && !repos.some((r) => r.repo === chosen)"
      ref="selection"
      class="co-hub-repo selected co-standalone-selection co-hub-selection"
      aria-label="社区模型版本"
    >
      <div class="co-hub-repo-main">
        <div class="co-hub-repo-body">
          <div class="co-hub-repo-title">
            <h3>{{ chosen }}</h3>
          </div>
        </div>
        <AButton
          type="link"
          size="small"
          class="co-inline-link"
          @click="colabOneOpenURL('https://huggingface.co/' + chosen)"
        >
          查看仓库
          <PhArrowSquareOut :size="12" />
        </AButton>
      </div>

      <div v-if="filesLoading" class="co-picker-loading" role="status">
        <ASpin size="small" tip="正在读取版本清单…" />
      </div>

      <AAlert
        v-else-if="fileError"
        type="error"
        show-icon
        :message="fileError"
        role="alert"
      >
        <template #action>
          <AButton size="small" @click="choose(chosen, true)">重试</AButton>
        </template>
      </AAlert>

      <template v-else>
        <template v-if="files.length">
          <div class="co-picker-row">
            <ASelect
              :value="filename || undefined"
              aria-label="选择量化版本"
              placeholder="选择量化版本"
              class="co-picker-select"
              @change="onFilenameChange"
            >
              <ASelectOption
                v-for="file in files"
                :key="file.filename"
                :value="file.filename"
                :disabled="!file.supported"
              >
                {{ file.filename }} · {{ size(file.size) }} ·
                {{
                  file.shards > 1 ? `${file.shards} 个分片文件` : "1 个文件"
                }}{{
                  fileSavedTag(file.filename)
                    ? ` (${fileSavedTag(file.filename)})`
                    : ""
                }}{{ file.reason ? " · " + file.reason : "" }}
              </ASelectOption>
            </ASelect>
          </div>

          <div v-if="selectedFile" class="co-hub-download-summary">
            <div class="co-summary-meta">
              <ATag
                v-if="selectedFile.quantization"
                color="blue"
                :bordered="false"
              >
                {{ selectedFile.quantization }}
              </ATag>
              <span class="co-tabular">
                <strong>{{ size(selectedFile.size) }}</strong>
              </span>
              <span>·</span>
              <span>
                {{
                  selectedFile.shards > 1
                    ? `共 ${selectedFile.shards} 个文件`
                    : "共 1 个文件"
                }}
              </span>
              <ATag v-if="selectedSavedTag" color="blue" :bordered="false">
                {{ selectedSavedTag }}
              </ATag>
            </div>
            <AButton
              type="link"
              size="small"
              class="co-inline-link co-advanced-toggle"
              @click="showAdvanced = !showAdvanced"
            >
              <PhSlidersHorizontal :size="13" />
              <span>{{ showAdvanced ? "收起设置" : "名称与参数" }}</span>
            </AButton>
          </div>

          <div v-if="showAdvanced" class="co-hub-advanced-panel">
            <div class="co-advanced-field">
              <label class="co-picker-label">显示名称（可选）</label>
              <AInput
                v-model:value="customName"
                size="small"
                :placeholder="chosen.split('/').pop()"
                aria-label="本地显示名称"
                allow-clear
              />
            </div>
            <div class="co-advanced-inline">
              <div class="co-advanced-field">
                <span class="co-picker-label">上下文</span>
                <ASelect
                  :value="contextLength"
                  aria-label="上下文长度"
                  size="small"
                  class="co-param-context-select"
                  @change="onContextLengthChange"
                >
                  <ASelectOption
                    v-for="opt in CONTEXT_OPTIONS"
                    :key="opt.value"
                    :value="opt.value"
                  >
                    {{ opt.label }}
                  </ASelectOption>
                </ASelect>
              </div>
              <div class="co-advanced-field">
                <span class="co-picker-label">温度</span>
                <AInputNumber
                  :value="temperature"
                  :min="0"
                  :max="2"
                  :step="0.1"
                  size="small"
                  aria-label="温度参数"
                  class="co-param-temp-input"
                  @update:value="onTemperatureUpdate"
                />
              </div>
            </div>
          </div>

          <ASpace wrap :size="8" class="co-hub-install-actions">
            <template v-if="ready">
              <AButton
                type="primary"
                size="small"
                :loading="installing"
                :disabled="disabled || installing || !filename"
                @click="install('load')"
              >
                <template #icon><PhPlay :size="13" weight="fill" /></template>
                {{ installing ? "正在提交…" : "下载并装载" }}
              </AButton>
              <AButton
                size="small"
                :disabled="disabled || installing || !filename"
                @click="install('download')"
              >
                <template #icon><PhDownloadSimple :size="14" /></template>
                仅下载
              </AButton>
              <AButton
                size="small"
                :disabled="installing || !filename"
                @click="install('save')"
              >
                <template #icon><PhPushPin :size="14" /></template>
                加入我的
              </AButton>
              <AButton
                v-if="loadedModelFor(chosen)"
                type="primary"
                ghost
                size="small"
                @click="emit('useLoaded')"
              >
                <template #icon><PhCheck :size="13" /></template>
                使用中
              </AButton>
            </template>
            <template v-else>
              <AButton
                type="primary"
                size="small"
                :loading="installing"
                :disabled="installing || !filename"
                @click="install('save')"
              >
                <template #icon><PhPushPin :size="14" /></template>
                加入我的
              </AButton>
            </template>
          </ASpace>
        </template>

        <AEmpty
          v-else
          description="该仓库暂无可用的 GGUF 模型文件"
          class="co-picker-empty"
        />
      </template>
    </div>

    <div
      v-if="loading && !repos.length"
      class="co-hub-loading-state"
      role="status"
    >
      <ASpin tip="正在搜索社区模型…" />
    </div>

    <div
      v-else-if="!loading && !error && !repos.length && !chosen"
      class="co-hub-empty-state"
    >
      <AEmpty description="没有找到匹配的社区 GGUF 模型，换个关键词试试" />
    </div>

    <div v-if="repos.length" class="co-hub-results">
      <div
        v-for="repo in repos"
        :key="repo.repo"
        class="co-hub-repo"
        :class="{ selected: chosen === repo.repo }"
      >
        <div class="co-hub-repo-main">
          <div class="co-hub-repo-body">
            <div class="co-hub-repo-title">
              <h3>
                <span v-if="repoAuthor(repo.repo)" class="co-repo-author"
                  >{{ repoAuthor(repo.repo) }}/</span
                >{{ repoShortName(repo.repo) }}
              </h3>
              <ATag
                v-if="summaryFor(repo.repo).loadedName"
                color="success"
                :bordered="false"
              >
                使用中
              </ATag>
              <ATag
                v-else-if="summaryFor(repo.repo).downloadedCount > 0"
                color="blue"
                :bordered="false"
              >
                已下载
              </ATag>
              <ATag v-if="repo.gated" color="orange" :bordered="false">
                <PhLock :size="11" />
                <span>需授权</span>
              </ATag>
            </div>

            <div class="co-hub-facts co-tabular">
              <span class="co-hub-size">
                <strong v-if="repo.sizeState === 'ready'">
                  {{ size(repo.minSize!)
                  }}<template v-if="repo.maxSize !== repo.minSize">
                    – {{ size(repo.maxSize!) }}</template
                  >
                </strong>
                <strong v-else>
                  {{ repo.sizeState === "unavailable" ? "大小未知" : "读取中…" }}
                </strong>
              </span>
              <span>·</span>
              <span class="co-hub-stat">
                <PhDownloadSimple :size="12" />
                {{ repo.downloads.toLocaleString() }}
              </span>
              <span>·</span>
              <span class="co-hub-stat">
                <PhHeart :size="12" />
                {{ repo.likes.toLocaleString() }}
              </span>
              <template v-if="repo.versions">
                <span>·</span>
                <span>{{ repo.versions }} 个版本</span>
              </template>
            </div>
          </div>

          <ASpace :size="6" class="co-hub-repo-actions">
            <AButton
              size="small"
              :class="{ 'co-common-active': isRepoMarked(repo.repo) }"
              :aria-pressed="isRepoMarked(repo.repo)"
              :aria-label="
                isRepoMarked(repo.repo)
                  ? `移出我的 ${repo.repo}`
                  : `加入我的 ${repo.repo}`
              "
              :loading="togglingRepo === repo.repo"
              @click="toggleCommonRepo(repo)"
            >
              <template #icon>
                <PhPushPinSlash v-if="isRepoMarked(repo.repo)" :size="13" />
                <PhPushPin v-else :size="13" />
              </template>
              {{ isRepoMarked(repo.repo) ? "已在我的" : "加入我的" }}
            </AButton>

            <AButton
              :type="chosen === repo.repo ? 'primary' : 'default'"
              :ghost="chosen === repo.repo"
              size="small"
              :aria-expanded="chosen === repo.repo"
              @click="choose(repo.repo)"
            >
              {{ chosen === repo.repo ? "收起" : "选择版本" }}
              <PhCaretDown v-if="chosen === repo.repo" :size="12" />
              <PhCaretRight v-else :size="12" />
            </AButton>
          </ASpace>
        </div>

        <div
          v-if="chosen === repo.repo"
          ref="selection"
          class="co-hub-selection co-inline-selection"
          aria-label="社区模型版本"
        >
          <div v-if="filesLoading" class="co-picker-loading" role="status">
            <ASpin size="small" tip="正在读取版本清单…" />
          </div>

          <AAlert
            v-else-if="fileError"
            type="error"
            show-icon
            :message="fileError"
            role="alert"
          >
            <template #action>
              <AButton size="small" @click="choose(chosen, true)">重试</AButton>
            </template>
          </AAlert>

          <template v-else>
            <template v-if="files.length">
              <div class="co-picker-row">
                <ASelect
                  :value="filename || undefined"
                  aria-label="选择量化版本"
                  placeholder="选择量化版本"
                  class="co-picker-select"
                  @change="onFilenameChange"
                >
                  <ASelectOption
                    v-for="file in files"
                    :key="file.filename"
                    :value="file.filename"
                    :disabled="!file.supported"
                  >
                    {{ file.filename }} · {{ size(file.size) }} ·
                    {{
                      file.shards > 1 ? `${file.shards} 个分片文件` : "1 个文件"
                    }}{{
                      fileSavedTag(file.filename)
                        ? ` (${fileSavedTag(file.filename)})`
                        : ""
                    }}{{ file.reason ? " · " + file.reason : "" }}
                  </ASelectOption>
                </ASelect>
              </div>

              <div v-if="selectedFile" class="co-hub-download-summary">
                <div class="co-summary-meta">
                  <ATag
                    v-if="selectedFile.quantization"
                    color="blue"
                    :bordered="false"
                  >
                    {{ selectedFile.quantization }}
                  </ATag>
                  <span class="co-tabular">
                    <strong>{{ size(selectedFile.size) }}</strong>
                  </span>
                  <span>·</span>
                  <span>
                    {{
                      selectedFile.shards > 1
                        ? `共 ${selectedFile.shards} 个文件`
                        : "共 1 个文件"
                    }}
                  </span>
                  <ATag v-if="selectedSavedTag" color="blue" :bordered="false">
                    {{ selectedSavedTag }}
                  </ATag>
                  <AButton
                    type="link"
                    size="small"
                    class="co-inline-link"
                    :aria-label="`打开 ${repo.repo} 仓库页面`"
                    @click="colabOneOpenURL('https://huggingface.co/' + repo.repo)"
                  >
                    仓库主页
                    <PhArrowSquareOut :size="12" />
                  </AButton>
                </div>

                <AButton
                  type="link"
                  size="small"
                  class="co-inline-link co-advanced-toggle"
                  @click="showAdvanced = !showAdvanced"
                >
                  <PhSlidersHorizontal :size="13" />
                  <span>{{ showAdvanced ? "收起设置" : "名称与参数" }}</span>
                </AButton>
              </div>

              <div v-if="showAdvanced" class="co-hub-advanced-panel">
                <div class="co-advanced-field">
                  <label class="co-picker-label">显示名称（可选）</label>
                  <AInput
                    v-model:value="customName"
                    size="small"
                    :placeholder="chosen.split('/').pop()"
                    aria-label="本地显示名称"
                    allow-clear
                  />
                </div>
                <div class="co-advanced-inline">
                  <div class="co-advanced-field">
                    <span class="co-picker-label">上下文</span>
                    <ASelect
                      :value="contextLength"
                      aria-label="上下文长度"
                      size="small"
                      class="co-param-context-select"
                      @change="onContextLengthChange"
                    >
                      <ASelectOption
                        v-for="opt in CONTEXT_OPTIONS"
                        :key="opt.value"
                        :value="opt.value"
                      >
                        {{ opt.label }}
                      </ASelectOption>
                    </ASelect>
                  </div>
                  <div class="co-advanced-field">
                    <span class="co-picker-label">温度</span>
                    <AInputNumber
                      :value="temperature"
                      :min="0"
                      :max="2"
                      :step="0.1"
                      size="small"
                      aria-label="温度参数"
                      class="co-param-temp-input"
                      @update:value="onTemperatureUpdate"
                    />
                  </div>
                </div>
              </div>

              <ASpace wrap :size="8" class="co-hub-install-actions">
                <template v-if="ready">
                  <AButton
                    type="primary"
                    size="small"
                    :loading="installing"
                    :disabled="disabled || installing || !filename"
                    @click="install('load')"
                  >
                    <template #icon
                      ><PhPlay :size="13" weight="fill"
                    /></template>
                    {{ installing ? "正在提交…" : "下载并装载" }}
                  </AButton>
                  <AButton
                    size="small"
                    :disabled="disabled || installing || !filename"
                    @click="install('download')"
                  >
                    <template #icon><PhDownloadSimple :size="14" /></template>
                    仅下载
                  </AButton>
                  <AButton
                    size="small"
                    :disabled="installing || !filename"
                    @click="install('save')"
                  >
                    <template #icon><PhPushPin :size="14" /></template>
                    加入我的
                  </AButton>
                  <AButton
                    v-if="loadedModelFor(repo.repo)"
                    type="primary"
                    ghost
                    size="small"
                    @click="emit('useLoaded')"
                  >
                    <template #icon><PhCheck :size="13" /></template>
                    使用中
                  </AButton>
                </template>
                <template v-else>
                  <AButton
                    type="primary"
                    size="small"
                    :loading="installing"
                    :disabled="installing || !filename"
                    @click="install('save')"
                  >
                    <template #icon><PhPushPin :size="14" /></template>
                    加入我的
                  </AButton>
                </template>
              </ASpace>
            </template>

            <AEmpty
              v-else
              description="该仓库暂无可用的 GGUF 模型文件"
              class="co-picker-empty"
            />
          </template>
        </div>
      </div>
    </div>

    <div v-if="cursor" class="co-hub-more-wrap">
      <AButton
        size="small"
        :loading="loading"
        :disabled="loading"
        class="co-hub-more"
        @click="search(true)"
      >
        {{ loading ? "加载中…" : "加载更多" }}
      </AButton>
    </div>
  </div>
</template>

<style scoped>
.co-hub-browser {
  --co-common-tone: #2563eb;
  --co-common-bg: #eff6ff;
  --co-common-border: #bfdbfe;
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
}

.co-hub-search-bar {
  display: flex;
  align-items: center;
  gap: 8px;
}

.co-hub-search-input {
  flex: 1;
  min-width: 0;
}

.co-search-prefix-icon {
  color: var(--co-muted);
}

.co-hub-sort-select {
  width: 112px;
  flex-shrink: 0;
}

.co-hub-loading-state,
.co-hub-empty-state {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 36px 16px;
  border: 1px solid var(--co-border-secondary);
  border-radius: 8px;
  background: var(--co-surface);
}

.co-hub-results {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-hub-repo {
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  color: var(--co-text);
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.co-hub-repo:hover {
  border-color: var(--co-border);
  box-shadow: var(--co-card-shadow);
}

.co-hub-repo.selected {
  border-color: var(--co-focus);
}

.co-hub-repo-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 0;
}

.co-hub-repo-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-hub-repo-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.co-hub-repo-title h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-repo-author {
  font-weight: 450;
  color: var(--co-muted);
}

.co-hub-facts {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 12px;
  color: var(--co-muted);
}

.co-hub-size strong {
  font-weight: 600;
  color: var(--co-secondary-text);
}

.co-hub-stat {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.co-hub-repo-actions {
  flex-shrink: 0;
}

.co-common-active {
  color: var(--co-common-tone);
  background: var(--co-common-bg);
  border-color: var(--co-common-border);
}

.co-inline-selection {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-standalone-selection {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-picker-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 14px 8px;
}

.co-picker-row {
  width: 100%;
}

.co-picker-select {
  width: 100%;
}

.co-picker-empty {
  margin: 8px 0;
}

.co-hub-download-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-summary-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.co-summary-meta strong {
  color: var(--co-text);
  font-weight: 600;
}

.co-advanced-toggle {
  font-size: 12px !important;
  color: var(--co-muted);
}

.co-hub-advanced-panel {
  padding: 10px 12px;
  border-radius: 6px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-advanced-inline {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}

.co-advanced-field {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.co-advanced-field:first-child {
  flex: 1;
}

.co-picker-label {
  font-size: 12px;
  color: var(--co-secondary-text);
  white-space: nowrap;
}

.co-param-context-select {
  width: 112px;
}

.co-param-temp-input {
  width: 80px;
}

.co-hub-install-actions {
  padding-top: 2px;
}

.co-hub-more-wrap {
  display: flex;
  justify-content: center;
  padding: 4px 0;
}

@media (max-width: 560px) {
  .co-hub-repo-main {
    flex-wrap: wrap;
  }

  .co-hub-repo-actions {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>

<style>
:root[data-theme="dark"] .co-hub-browser {
  --co-common-tone: #69b1ff;
  --co-common-bg: #112545;
  --co-common-border: #1d4e89;
}
</style>
