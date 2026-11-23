<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import { colabOneLocaleCode, t, tb } from "./i18n";
import {
  Alert as AAlert,
  Button as AButton,
  Empty as AEmpty,
  Input as AInput,
  Select as ASelect,
  SelectOption as ASelectOption,
  Space as ASpace,
  Tag as ATag,
} from "ant-design-vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import {
  PhArrowRight,
  PhArrowSquareOut,
  PhCaretDown,
  PhCaretRight,
  PhChatText,
  PhDownloadSimple,
  PhFlowArrow,
  PhHeart,
  PhImage,
  PhLock,
  PhMagnifyingGlass,
  PhPushPin,
  PhPushPinSlash,
  PhSquaresFour,
  PhVideoCamera,
} from "@phosphor-icons/vue";
import ModelVersionPicker from "./ModelVersionPicker.vue";
import { colabOneCommand, colabOneOpenURL } from "./api";
import type { CommonRepo, ModelSpec, RuntimeStatus } from "./types";
import { repoRemoteSummary } from "./modelLibrary";
import {
  COLABONE_HUB_FAMILIES_BY_MODALITY,
  COLABONE_HUB_MODALITIES,
  formatColabOneFileSize,
  modalityBadgeMeta,
  summarizeColabOneRepoFit,
  type ColabOneFileDiskState,
  type ColabOneGpuTarget,
  type ColabOneGpuTargetId,
  type ColabOneHubFile,
  type ColabOneHubModality,
  type ColabOneRepoFitSummary,
} from "./modelCompatibility";

const props = defineProps<{
  ready: boolean;
  disabled: boolean;
  commonRepos?: CommonRepo[];
  initialQuery?: string;
  initialRepo?: string;
  models?: ModelSpec[];
  runtime?: RuntimeStatus;
  targetGpu: ColabOneGpuTarget;
  onlyRunnable?: boolean;
  downloadDisabledReason?: string;
}>();

const emit = defineEmits<{
  installed: [mode: "save" | "download" | "load"];
  commonRepoUpdated: [message: string];
  toggleCommon: [model: ModelSpec];
  useLoaded: [];
  switchGpu: [ColabOneGpuTargetId];
  clearOnlyRunnable: [];
  goComfyui: [];
}>();

type Repo = {
  repo: string;
  downloads: number;
  likes: number;
  gated: boolean;
  pipeline?: string;
  modality?: "text" | "image" | "video";
  comfy_only?: boolean;
  family?: string;
  params?: string;
  minSize?: number;
  maxSize?: number;
  versions?: number;
  files?: ColabOneHubFile[];
  sizeState?: "loading" | "ready" | "unavailable";
};

type Detail = {
  revision: string;
  pipeline?: string;
  modality?: "text" | "image" | "video";
  comfy_only?: boolean;
  family?: string;
  params?: string;
  files: ColabOneHubFile[];
};

const REPO_PATTERN = /^[\w.-]+\/[\w.-]+$/;
const HF_URL_PATTERN =
  /^https?:\/\/(?:www\.)?huggingface\.co\/([\w.-]+\/[\w.-]+)(?:\/(?:blob|resolve|tree)\/([^/?#]+)(?:\/([^?#]+))?)?\/?(?:\?[^#]*)?(?:#.*)?$/i;

const HUB_SKELETON_ROWS = [
  { titleW: "56%", paramW: 34, sizeW: 92, fitW: 66, statW: 54 },
  { titleW: "48%", paramW: 40, sizeW: 84, fitW: 72, statW: 48 },
  { titleW: "62%", paramW: 32, sizeW: 96, fitW: 64, statW: 58 },
  { titleW: "52%", paramW: 38, sizeW: 88, fitW: 68, statW: 50 },
  { titleW: "44%", paramW: 36, sizeW: 80, fitW: 62, statW: 52 },
] as const;

const selection = ref<HTMLElement | HTMLElement[] | null>(null);
const query = ref(props.initialRepo || props.initialQuery || "");
const sort = ref("downloads");
const modality = ref<ColabOneHubModality>("text");
const family = ref("");
const repos = ref<Repo[]>([]);
const cursor = ref("");
const loading = ref(false);
const error = ref("");
const chosen = ref("");
const chosenModality = ref<"text" | "image" | "video">("text");
const chosenComfyOnly = ref(false);
const customName = ref("");
const revision = ref("");
const files = ref<ColabOneHubFile[]>([]);
const filename = ref("");
const preferredFilename = ref("");
const filesLoading = ref(false);
const fileError = ref("");
const installing = ref(false);
const togglingRepo = ref("");
const contextLength = ref<number>(8192);
const temperature = ref<number>(0.7);

const effectiveRuntime = computed<RuntimeStatus>(
  () => props.runtime || { state: props.ready ? "ready" : "stopped" },
);

const familyOptions = computed(
  () =>
    COLABONE_HUB_FAMILIES_BY_MODALITY[modality.value] ||
    COLABONE_HUB_FAMILIES_BY_MODALITY.text,
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
    return t("models.badgeDownloaded");
  }
  if (saved.common) {
    return t("models.badgeInMine");
  }
  return "";
}

function modelDiskStateFor(saved: ModelSpec): ColabOneFileDiskState {
  if (effectiveRuntime.value.state === "stopped") {
    return {};
  }
  const running = effectiveRuntime.value.tasks?.find(
    (item) => item.state === "running" && item.model_id === saved.id,
  );
  if (
    saved.downloaded ||
    saved.loaded ||
    effectiveRuntime.value.loaded_model === saved.id ||
    running?.kind === "load"
  ) {
    return { downloaded: true };
  }
  const dlBytes =
    running?.kind === "download"
      ? running.download?.downloaded_bytes ||
        Math.round(Math.min(1, running.progress || 0) * (saved.size || 0))
      : 0;
  const cachedBytes = Math.max(saved.cached_bytes || 0, dlBytes);
  return cachedBytes > 0 ? { cachedBytes } : {};
}

function fileDiskState(fileName: string): ColabOneFileDiskState | undefined {
  const saved = matchedSavedModel(fileName);
  return saved ? modelDiskStateFor(saved) : undefined;
}

function repoDiskStatesMap(repoName: string): Map<string, ColabOneFileDiskState> {
  const map = new Map<string, ColabOneFileDiskState>();
  const summary = summaryFor(repoName);
  for (const m of summary.savedModels) {
    const st = modelDiskStateFor(m);
    for (const f of m.files || []) {
      const prev = map.get(f.path);
      if (
        !prev ||
        st.downloaded ||
        (!prev.downloaded && (st.cachedBytes || 0) > (prev.cachedBytes || 0))
      ) {
        map.set(f.path, st);
      }
    }
  }
  return map;
}

const fitByRepo = computed(() => {
  const map = new Map<string, ColabOneRepoFitSummary>();
  for (const r of repos.value) {
    if (r.sizeState === "ready" && r.files?.length) {
      const comfyOnly = Boolean(
        r.comfy_only || r.modality === "image" || r.modality === "video",
      );
      map.set(
        r.repo,
        summarizeColabOneRepoFit(
          r.files,
          props.targetGpu,
          8192,
          comfyOnly,
          repoDiskStatesMap(r.repo),
        ),
      );
    }
  }
  return map;
});

function repoFitFor(repoName: string): ColabOneRepoFitSummary | null {
  return fitByRepo.value.get(repoName) || null;
}

const visibleRepos = computed(() => {
  if (!props.onlyRunnable || props.targetGpu.vramMib <= 0) {
    return repos.value;
  }
  return repos.value.filter((r) => {
    if (r.sizeState !== "ready") return true;
    const fit = fitByRepo.value.get(r.repo);
    return !fit || fit.runnable;
  });
});

const hiddenByVramCount = computed(() =>
  Math.max(0, repos.value.length - visibleRepos.value.length),
);

let searchGeneration = 0;
let previewGeneration = 0;
let fileGeneration = 0;
let timer: ReturnType<typeof setTimeout>;
const details = new Map<string, Detail>();
const pendingDetails = new Map<string, Promise<Detail>>();

async function openExternalURL(url: string) {
  try {
    await colabOneOpenURL(url);
  } catch (e) {
    error.value = String(e);
  }
}

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
    if (id !== previewGeneration) return;
    const row = repos.value.find((item) => item.repo === repo);
    if (!row) return;
    const sizes = (result.files || [])
      .filter((f) => f.supported && f.size > 0)
      .map((f) => f.size);
    const patch: Partial<Repo> = {
      files: result.files || [],
      ...(result.modality && !row.modality ? { modality: result.modality } : {}),
      ...(typeof result.comfy_only === "boolean" && row.comfy_only === undefined
        ? { comfy_only: result.comfy_only }
        : {}),
      ...(result.params && !row.params ? { params: result.params } : {}),
    };
    Object.assign(
      row,
      patch,
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
    if (id === previewGeneration) {
      const row = repos.value.find((item) => item.repo === repo);
      if (row) row.sizeState = "unavailable";
    }
  }
}

async function previewSizes(batch: Repo[], id: number) {
  let index = 0;
  async function worker() {
    while (id === previewGeneration && index < batch.length) {
      const repo = batch[index++]!.repo;
      await previewSize(repo, id);
    }
  }
  await Promise.all([worker(), worker(), worker()]);
}

async function search(more = false): Promise<boolean> {
  const id = ++searchGeneration;
  if (!more) {
    previewGeneration += 1;
  }
  const pGen = previewGeneration;
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
      modality: modality.value,
      family: family.value,
    });
    if (id !== searchGeneration) return false;
    const incoming: Repo[] = (result.models || []).map((r: Repo) => ({
      ...r,
      modality: r.modality || "text",
      comfy_only:
        typeof r.comfy_only === "boolean"
          ? r.comfy_only
          : r.modality === "image" || r.modality === "video",
      sizeState: "loading" as const,
    }));
    const existingSet = new Set(repos.value.map((r) => r.repo));
    const freshIncoming = more
      ? incoming.filter((r) => !existingSet.has(r.repo))
      : incoming;
    const all = more ? [...repos.value, ...freshIncoming] : incoming;
    repos.value = [
      ...new Map<string, Repo>(all.map((r: Repo) => [r.repo, r])).values(),
    ];
    cursor.value = result.next_cursor || "";
    void previewSizes(
      more
        ? freshIncoming
        : repos.value.filter((repo) => repo.sizeState === "loading"),
      pGen,
    );
    return true;
  } catch (e) {
    if (id === searchGeneration) error.value = String(e);
    return false;
  } finally {
    if (id === searchGeneration) loading.value = false;
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
    void search(false).then((ok) => {
      if (ok) void choose(targetRepo, true, targetFile);
    });
    return;
  }
  if (REPO_PATTERN.test(trimmed)) {
    void search(false).then((ok) => {
      if (ok) void choose(trimmed, true);
    });
    return;
  }
  void search(false);
}

function onSortChange(val: unknown) {
  sort.value = String(val || "downloads");
}

function selectModality(next: ColabOneHubModality) {
  if (modality.value === next) return;
  modality.value = next;
  family.value = "";
}

function selectFamily(nextId: string) {
  family.value = family.value === nextId ? "" : nextId;
}

let suppressQueryWatch = false;
let initialSearchDone = false;

watch(
  [query, sort, modality, family],
  ([nextQuery], prev) => {
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
      void search(false).then((ok) => {
        if (ok) void choose(targetRepo, true, targetFile);
      });
      return;
    }
    ++searchGeneration;
    ++previewGeneration;
    loading.value = true;
    error.value = "";
    cursor.value = "";
    repos.value = [];
    ++fileGeneration;
    chosen.value = "";
    files.value = [];
    filename.value = "";
    preferredFilename.value = "";
    filesLoading.value = false;
    clearTimeout(timer);
    if (!initialSearchDone) {
      initialSearchDone = true;
      const bootRepo = props.initialRepo;
      if (bootRepo) {
        void search(false).then((ok) => {
          if (ok) void choose(bootRepo, true);
        });
      } else {
        void search();
      }
      return;
    }
    const queryChanged = !prev || nextQuery !== prev[0];
    if (!queryChanged) {
      void search();
      return;
    }
    timer = setTimeout(() => search(), 300);
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
      void search(false).then((ok) => {
        if (ok) void choose(repo, true);
      });
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
);

async function choose(repo: string, forceOpen = false, preferredFile = "") {
  if (!forceOpen && chosen.value === repo) {
    ++fileGeneration;
    chosen.value = "";
    files.value = [];
    filename.value = "";
    preferredFilename.value = "";
    filesLoading.value = false;
    fileError.value = "";
    return;
  }
  const id = ++fileGeneration;
  const row = repos.value.find((r) => r.repo === repo);
  chosen.value = repo;
  chosenModality.value = row?.modality || "text";
  chosenComfyOnly.value = !!row?.comfy_only;
  customName.value = "";
  files.value = [];
  filename.value = preferredFile || "";
  preferredFilename.value = preferredFile || "";
  revision.value = "";
  fileError.value = "";
  filesLoading.value = true;
  try {
    const result = await detail(repo);
    if (id !== fileGeneration) return;
    revision.value = result.revision;
    files.value = result.files || [];
    chosenModality.value = result.modality || row?.modality || "text";
    chosenComfyOnly.value =
      typeof result.comfy_only === "boolean"
        ? result.comfy_only
        : chosenModality.value === "image" || chosenModality.value === "video";
    if (
      preferredFile &&
      files.value.some((f) => f.supported && f.filename === preferredFile)
    ) {
      filename.value = preferredFile;
    }
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
  if (togglingRepo.value || repo.comfy_only) return;
  const summary = summaryFor(repo.repo);
  const inCommonRepos = isCommonRepo(repo.repo);
  togglingRepo.value = repo.repo;
  try {
    if (!inCommonRepos && summary.common) {
      for (const m of summary.savedModels) {
        if (m.common) {
          await colabOneCommand("model.common", { id: m.id, common: false });
        }
      }
      emit(
        "commonRepoUpdated",
        t("models.toastRemovedFromMine", { name: repo.repo }),
      );
      return;
    }
    const res = await colabOneCommand("common_repo.save", {
      repo: repo.repo,
      name: repo.repo.split("/").pop() || repo.repo,
      downloads: repo.downloads,
      likes: repo.likes,
      toggle: true,
    });
    if (inCommonRepos && !res?.common && summary.common) {
      for (const m of summary.savedModels) {
        if (m.common) {
          await colabOneCommand("model.common", { id: m.id, common: false });
        }
      }
    }
    emit(
      "commonRepoUpdated",
      res?.common
        ? t("models.toastAddedToMine", { name: repo.repo })
        : t("models.toastRemovedFromMine", { name: repo.repo }),
    );
  } catch (e) {
    error.value = String(e);
  } finally {
    togglingRepo.value = "";
  }
}

async function install(mode: "save" | "download" | "load" = "load") {
  if (!filename.value || installing.value || chosenComfyOnly.value) return;
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

function fitBadgeColor(fit: ColabOneRepoFitSummary): string {
  if (fit.status === "compatible") return "success";
  if (fit.status === "tight") return "warning";
  if (fit.status === "disk_overflow") return "error";
  if (fit.status === "hybrid" || fit.status === "heavy_hybrid") {
    return "default";
  }
  return "blue";
}

onUnmounted(() => {
  ++searchGeneration;
  ++previewGeneration;
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
        :aria-label="t('models.hubSearchAria')"
        :placeholder="t('models.hubSearchPlaceholder')"
        class="co-hub-search-input"
        @press-enter="submitSearch"
      >
        <template #prefix>
          <PhMagnifyingGlass :size="15" class="co-search-prefix-icon" />
        </template>
      </AInput>

      <ASelect
        :value="sort"
        :aria-label="t('models.hubSortAria')"
        class="co-hub-sort-select"
        :dropdown-match-select-width="false"
        @change="onSortChange"
      >
        <ASelectOption value="downloads">{{ t("models.hubSortDownloads") }}</ASelectOption>
        <ASelectOption value="likes">{{ t("models.hubSortLikes") }}</ASelectOption>
        <ASelectOption value="updated">{{ t("models.hubSortUpdated") }}</ASelectOption>
      </ASelect>
    </div>

    <div class="co-hub-filter-panel">
      <div class="co-filter-row" role="group" :aria-label="t('models.hubModalityAria')">
        <span class="co-filter-label">{{ t("models.hubModalityLabel") }}</span>
        <div class="co-chip-list">
          <button
            v-for="opt in COLABONE_HUB_MODALITIES"
            :key="opt.id"
            type="button"
            class="co-filter-chip"
            :class="{ active: modality === opt.id }"
            :aria-pressed="modality === opt.id"
            @click="selectModality(opt.id)"
          >
            <PhChatText v-if="opt.id === 'text'" :size="13" />
            <PhImage v-else-if="opt.id === 'image'" :size="13" />
            <PhVideoCamera v-else-if="opt.id === 'video'" :size="13" />
            <PhSquaresFour v-else :size="13" />
            <span>{{ opt.label }}</span>
            <span v-if="opt.comfyOnly" class="co-chip-sub">ComfyUI</span>
          </button>
        </div>
      </div>

      <div class="co-filter-row" role="group" :aria-label="t('models.hubFamilyAria')">
        <span class="co-filter-label">{{ t("models.hubFamilyLabel") }}</span>
        <div class="co-chip-list">
          <button
            v-for="fam in familyOptions"
            :key="fam.id || '__all'"
            type="button"
            class="co-filter-chip co-family-chip"
            :class="{ active: family === fam.id }"
            :aria-pressed="family === fam.id"
            @click="selectFamily(fam.id)"
          >
            {{ fam.label }}
          </button>
        </div>
      </div>
    </div>

    <div
      v-if="modality === 'image' || modality === 'video'"
      class="co-comfy-modality-banner"
      role="note"
    >
      <div class="co-comfy-banner-text">
        <PhFlowArrow :size="15" class="co-comfy-banner-icon" />
        <span>
          {{
            t("models.hubComfyOnlyBanner", {
              modality:
                modality === "video"
                  ? t("models.compatModalityVideo")
                  : t("models.compatModalityImage"),
            })
          }}
        </span>
      </div>
      <AButton
        type="link"
        size="small"
        class="co-inline-link co-comfy-banner-link"
        @click="emit('goComfyui')"
      >
        {{ t("models.hubGoComfy") }}
        <PhArrowRight :size="13" />
      </AButton>
    </div>

    <AAlert
      v-if="error"
      type="error"
      show-icon
      :message="tb(error)"
      role="alert"
    >
      <template #action>
        <AButton size="small" @click="search()">{{ t("common.retry") }}</AButton>
      </template>
    </AAlert>

    <div
      v-if="chosen && !visibleRepos.some((r) => r.repo === chosen)"
      ref="selection"
      class="co-hub-repo selected co-standalone-selection co-hub-selection"
      :aria-label="t('models.hubVersionsAria')"
    >
      <div class="co-hub-repo-main">
        <div class="co-hub-repo-body">
          <div class="co-hub-repo-title">
            <h3>{{ chosen }}</h3>
            <ATag
              v-if="chosenComfyOnly"
              :color="modalityBadgeMeta(chosenModality).color"
              :bordered="false"
            >
              {{
                t("models.hubComfyOnlyTag", {
                  modality: modalityBadgeMeta(chosenModality).label,
                })
              }}
            </ATag>
          </div>
        </div>
        <AButton
          type="link"
          size="small"
          class="co-inline-link"
          @click="openExternalURL('https://huggingface.co/' + chosen)"
        >
          {{ t("models.hubViewRepo") }}
          <PhArrowSquareOut :size="12" />
        </AButton>
      </div>

      <ModelVersionPicker
        :repo="chosen"
        :files="files"
        :loading="filesLoading"
        :error="fileError"
        :modality="chosenModality"
        :comfy-only="chosenComfyOnly"
        :target-gpu="targetGpu"
        :runtime="effectiveRuntime"
        :ready="ready"
        :disabled="disabled"
        :installing="installing"
        :selected-filename="filename"
        :preferred-filename="preferredFilename"
        :context-length="contextLength"
        :temperature="temperature"
        :custom-name="customName"
        :allow-custom-name="true"
        :show-save-button="true"
        :show-use-loaded="!!loadedModelFor(chosen)"
        :saved-tag-for-file="fileSavedTag"
        :file-disk-state="fileDiskState"
        :download-disabled-reason="downloadDisabledReason"
        @update:selected-filename="(v) => (filename = v)"
        @update:context-length="(v) => (contextLength = v)"
        @update:temperature="(v) => (temperature = v)"
        @update:custom-name="(v) => (customName = v)"
        @retry="choose(chosen, true)"
        @install="install"
        @use-loaded="emit('useLoaded')"
        @switch-gpu="(id) => emit('switchGpu', id)"
        @go-comfyui="emit('goComfyui')"
      />
    </div>

    <div
      v-if="loading && !repos.length"
      class="co-hub-results co-hub-skeleton-list"
      role="status"
      aria-busy="true"
    >
      <span class="co-sr-only">{{ t("models.hubSearching") }}</span>
      <div
        v-for="(row, idx) in HUB_SKELETON_ROWS"
        :key="idx"
        class="co-hub-repo co-hub-repo-skeleton"
      >
        <div class="co-hub-repo-main">
          <div class="co-hub-repo-body">
            <div class="co-hub-repo-title">
              <ColabOneSkeleton
                :width="row.titleW"
                :height="16"
                variant="text"
                :delay="idx * 65"
              />
              <ColabOneSkeleton
                :width="row.paramW"
                :height="18"
                variant="tag"
                tone="subtle"
                :delay="idx * 65 + 45"
              />
            </div>
            <div class="co-hub-facts">
              <ColabOneSkeleton
                :width="row.sizeW"
                :height="13"
                variant="text"
                :delay="idx * 65 + 70"
              />
              <ColabOneSkeleton
                :width="row.fitW"
                :height="18"
                variant="tag"
                :delay="idx * 65 + 100"
              />
              <ColabOneSkeleton
                :width="row.statW"
                :height="12"
                variant="text"
                tone="subtle"
                :delay="idx * 65 + 130"
              />
              <ColabOneSkeleton
                :width="40"
                :height="12"
                variant="text"
                tone="subtle"
                :delay="idx * 65 + 150"
              />
            </div>
          </div>
          <div class="co-hub-repo-actions co-hub-sk-actions">
            <ColabOneSkeleton
              :width="68"
              :height="24"
              variant="button"
              :radius="6"
              tone="subtle"
              :delay="idx * 65 + 80"
            />
            <ColabOneSkeleton
              :width="82"
              :height="24"
              variant="button"
              :radius="6"
              :delay="idx * 65 + 120"
            />
          </div>
        </div>
      </div>
    </div>

    <div
      v-else-if="!loading && !error && !repos.length && !chosen"
      class="co-hub-empty-state"
    >
      <AEmpty :description="t('models.hubEmpty')" />
    </div>

    <div
      v-else-if="
        !loading && !error && repos.length > 0 && !visibleRepos.length && !chosen
      "
      class="co-hub-empty-state"
    >
      <AEmpty
        :description="
          t('models.hubAllOverflow', { count: repos.length, gpu: targetGpu.label })
        "
      >
        <ASpace :size="8" wrap>
          <AButton
            v-if="targetGpu.id !== 'G4' && targetGpu.vramGb < 96"
            type="primary"
            size="small"
            @click="emit('switchGpu', 'G4')"
          >
            {{ t("models.hubSwitchG4") }}
          </AButton>
          <AButton size="small" @click="emit('clearOnlyRunnable')">
            {{ t("models.hubShowAllModels") }}
          </AButton>
        </ASpace>
      </AEmpty>
    </div>

    <div v-if="visibleRepos.length" class="co-hub-results">
      <div
        v-for="repo in visibleRepos"
        :key="repo.repo"
        class="co-hub-repo"
        :class="{
          selected: chosen === repo.repo,
          'is-overflow':
            (repoFitFor(repo.repo)?.status === 'heavy_hybrid' ||
              repoFitFor(repo.repo)?.status === 'disk_overflow') &&
            !repo.comfy_only,
          'is-comfy-only': !!repo.comfy_only,
        }"
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
                v-if="repo.params"
                :bordered="false"
                class="co-repo-param-tag"
              >
                {{ repo.params }}
              </ATag>
              <ATag
                v-if="repo.comfy_only"
                :color="modalityBadgeMeta(repo.modality).color"
                :bordered="false"
              >
                {{
                  t("models.hubComfyOnlyTag", {
                    modality: modalityBadgeMeta(repo.modality).label,
                  })
                }}
              </ATag>
              <ATag
                v-if="summaryFor(repo.repo).loadedName"
                color="success"
                :bordered="false"
              >
                {{ t("models.badgeInUse") }}
              </ATag>
              <ATag
                v-else-if="summaryFor(repo.repo).downloadedCount > 0"
                color="blue"
                :bordered="false"
              >
                {{ t("models.badgeDownloaded") }}
              </ATag>
              <ATag v-if="repo.gated" color="orange" :bordered="false">
                <PhLock :size="11" />
                <span>{{ t("models.hubGatedTag") }}</span>
              </ATag>
            </div>

            <div class="co-hub-facts co-tabular">
              <span class="co-hub-size">
                <template v-if="repo.sizeState === 'ready'">
                  {{ t("models.pickerDiskLabel") }}
                  <strong class="co-sk-reveal">
                    {{ formatColabOneFileSize(repo.minSize!)
                    }}<template v-if="repo.maxSize !== repo.minSize">
                      – {{ formatColabOneFileSize(repo.maxSize!) }}</template
                    >
                  </strong>
                </template>
                <strong v-else-if="repo.sizeState === 'unavailable'">
                  {{ t("common.unknownSize") }}
                </strong>
                <span v-else class="co-hub-size-skeleton">
                  <ColabOneSkeleton
                    :width="82"
                    :height="13"
                    variant="text"
                    inline
                  />
                  <span class="co-sr-only">{{ t("common.reading") }}</span>
                </span>
              </span>

              <ATag
                v-if="repoFitFor(repo.repo)?.badgeLabel"
                :color="fitBadgeColor(repoFitFor(repo.repo)!)"
                :bordered="false"
                class="co-hub-fit-pill co-sk-reveal"
              >
                {{ repoFitFor(repo.repo)!.badgeLabel }}
              </ATag>
              <ColabOneSkeleton
                v-else-if="repo.sizeState === 'loading' && !repo.comfy_only"
                :width="64"
                :height="18"
                variant="tag"
                tone="subtle"
                :delay="90"
                inline
              />

              <span>·</span>
              <span class="co-hub-stat">
                <PhDownloadSimple :size="12" />
                {{ repo.downloads.toLocaleString(colabOneLocaleCode) }}
              </span>
              <span>·</span>
              <span class="co-hub-stat">
                <PhHeart :size="12" />
                {{ repo.likes.toLocaleString(colabOneLocaleCode) }}
              </span>
              <template v-if="repo.versions">
                <span>·</span>
                <span>{{
                  repo.versions === 1
                    ? t("models.hubVersionCountSingle", { count: repo.versions })
                    : t("models.hubVersionCount", { count: repo.versions })
                }}</span>
              </template>
            </div>
          </div>

          <ASpace :size="6" class="co-hub-repo-actions">
            <AButton
              v-if="!repo.comfy_only"
              size="small"
              :class="{ 'co-common-active': isRepoMarked(repo.repo) }"
              :aria-pressed="isRepoMarked(repo.repo)"
              :aria-label="
                isRepoMarked(repo.repo)
                  ? t('models.removeFromMineTarget', { name: repo.repo })
                  : t('models.addToMineTarget', { name: repo.repo })
              "
              :loading="togglingRepo === repo.repo"
              @click="toggleCommonRepo(repo)"
            >
              <template #icon>
                <PhPushPinSlash v-if="isRepoMarked(repo.repo)" :size="13" />
                <PhPushPin v-else :size="13" />
              </template>
              {{ isRepoMarked(repo.repo) ? t("models.badgeInMine") : t("models.addToMine") }}
            </AButton>

            <AButton
              :type="chosen === repo.repo ? 'primary' : 'default'"
              :ghost="chosen === repo.repo"
              size="small"
              :aria-expanded="chosen === repo.repo"
              @click="choose(repo.repo)"
            >
              {{
                chosen === repo.repo
                  ? t("common.collapse")
                  : repo.comfy_only
                    ? t("models.viewVram")
                    : t("models.chooseVersion")
              }}
              <PhCaretDown v-if="chosen === repo.repo" :size="12" />
              <PhCaretRight v-else :size="12" />
            </AButton>
          </ASpace>
        </div>

        <div
          v-if="chosen === repo.repo"
          ref="selection"
          class="co-hub-selection co-inline-selection"
          :aria-label="t('models.hubVersionsAria')"
        >
          <ModelVersionPicker
            :repo="repo.repo"
            :files="files"
            :loading="filesLoading"
            :error="fileError"
            :modality="chosenModality"
            :comfy-only="chosenComfyOnly"
            :target-gpu="targetGpu"
            :runtime="effectiveRuntime"
            :ready="ready"
            :disabled="disabled"
            :installing="installing"
            :selected-filename="filename"
            :preferred-filename="preferredFilename"
            :context-length="contextLength"
            :temperature="temperature"
            :custom-name="customName"
            :allow-custom-name="true"
            :show-save-button="true"
            :show-use-loaded="!!loadedModelFor(repo.repo)"
            :saved-tag-for-file="fileSavedTag"
            :file-disk-state="fileDiskState"
            :download-disabled-reason="downloadDisabledReason"
            @update:selected-filename="(v) => (filename = v)"
            @update:context-length="(v) => (contextLength = v)"
            @update:temperature="(v) => (temperature = v)"
            @update:custom-name="(v) => (customName = v)"
            @retry="choose(repo.repo, true)"
            @install="install"
            @use-loaded="emit('useLoaded')"
            @switch-gpu="(id) => emit('switchGpu', id)"
            @go-comfyui="emit('goComfyui')"
          />
        </div>
      </div>

      <template v-if="loading && repos.length > 0">
        <div
          v-for="(row, idx) in HUB_SKELETON_ROWS.slice(0, 2)"
          :key="`more-${idx}`"
          class="co-hub-repo co-hub-repo-skeleton"
          aria-hidden="true"
        >
          <div class="co-hub-repo-main">
            <div class="co-hub-repo-body">
              <div class="co-hub-repo-title">
                <ColabOneSkeleton
                  :width="row.titleW"
                  :height="16"
                  variant="text"
                  :delay="idx * 70"
                />
                <ColabOneSkeleton
                  :width="row.paramW"
                  :height="18"
                  variant="tag"
                  tone="subtle"
                  :delay="idx * 70 + 45"
                />
              </div>
              <div class="co-hub-facts">
                <ColabOneSkeleton
                  :width="row.sizeW"
                  :height="13"
                  variant="text"
                  :delay="idx * 70 + 70"
                />
                <ColabOneSkeleton
                  :width="row.fitW"
                  :height="18"
                  variant="tag"
                  :delay="idx * 70 + 105"
                />
                <ColabOneSkeleton
                  :width="row.statW"
                  :height="12"
                  variant="text"
                  tone="subtle"
                  :delay="idx * 70 + 135"
                />
              </div>
            </div>
            <div class="co-hub-repo-actions co-hub-sk-actions">
              <ColabOneSkeleton
                :width="68"
                :height="24"
                variant="button"
                :radius="6"
                tone="subtle"
                :delay="idx * 70 + 80"
              />
              <ColabOneSkeleton
                :width="82"
                :height="24"
                variant="button"
                :radius="6"
                :delay="idx * 70 + 115"
              />
            </div>
          </div>
        </div>
      </template>

      <div v-if="hiddenByVramCount > 0" class="co-hub-hidden-note">
        <span>
          {{
            t("models.hubHiddenOverflow", {
              count: hiddenByVramCount,
              gpu: targetGpu.shortLabel,
            })
          }}
        </span>
        <AButton
          type="link"
          size="small"
          class="co-inline-link"
          @click="emit('clearOnlyRunnable')"
        >
          {{ t("common.showAll") }}
        </AButton>
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
        {{ loading ? t("common.loading") : t("common.loadMore") }}
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
  gap: 10px;
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
  min-width: 148px;
  width: auto;
  flex-shrink: 0;
}

.co-hub-filter-panel {
  display: flex;
  flex-direction: column;
  gap: 7px;
  padding: 9px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-filter-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.co-filter-label {
  font-size: 11.5px;
  font-weight: 600;
  color: var(--co-muted);
  white-space: nowrap;
  flex-shrink: 0;
  min-width: 48px;
}

.co-chip-list {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
  min-width: 0;
}

.co-filter-chip {
  appearance: none;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 10px;
  border-radius: 9999px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  color: var(--co-secondary-text);
  font-size: 12px;
  line-height: 1.45;
  cursor: pointer;
  transition:
    border-color 0.14s ease,
    background-color 0.14s ease,
    color 0.14s ease;
}

.co-filter-chip:hover {
  border-color: var(--co-border);
  color: var(--co-text);
  background: var(--co-soft-hover);
}

.co-filter-chip.active {
  border-color: var(--co-accent);
  background: var(--co-selected);
  color: var(--co-accent-strong);
  font-weight: 600;
}

.co-chip-sub {
  font-size: 10px;
  opacity: 0.72;
  padding-left: 2px;
}

.co-comfy-modality-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  padding: 8px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-warning-border);
  background: var(--co-warning-bg);
  font-size: 12px;
  color: var(--co-text);
}

.co-comfy-banner-text {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  flex: 1;
  min-width: 200px;
  line-height: 1.45;
}

.co-comfy-banner-icon {
  color: var(--co-warning-text);
  flex-shrink: 0;
  margin-top: 2px;
}

.co-comfy-banner-link {
  font-weight: 600;
  flex-shrink: 0;
}

.co-hub-empty-state {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 36px 16px;
  border: 1px solid var(--co-border-secondary);
  border-radius: 8px;
  background: var(--co-surface);
}

.co-hub-repo-skeleton {
  background: var(--co-skeleton-card-bg);
  pointer-events: none;
}

.co-hub-repo-skeleton:hover {
  border-color: var(--co-border-secondary);
  box-shadow: none;
}

.co-hub-sk-actions {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-hub-size-skeleton {
  display: inline-flex;
  align-items: center;
  min-height: 18px;
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
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    opacity 0.16s ease;
}

.co-hub-repo:hover {
  border-color: var(--co-border);
  box-shadow: var(--co-card-shadow);
}

.co-hub-repo.selected {
  border-color: var(--co-focus);
}

.co-hub-repo.is-overflow:not(.selected) {
  opacity: 0.72;
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

.co-repo-param-tag,
.co-hub-fit-pill {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 1.4;
  padding: 0 6px;
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
}

.co-standalone-selection {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-hub-hidden-note {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 6px 10px;
  font-size: 12px;
  color: var(--co-muted);
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
