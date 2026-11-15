<script setup lang="ts">
import { ref, watch, onUnmounted, nextTick, computed } from "vue";
import { command, openURL } from "./api";
const props = defineProps<{ ready: boolean; disabled: boolean }>();
const emit = defineEmits<{ installed: []; device: [] }>();
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
type Detail = { revision: string; files: File[] };
type File = {
  filename: string;
  size: number;
  shards: number;
  quantization: string;
  supported: boolean;
  reason: string;
};
const selection = ref<HTMLElement | null>(null);
const query = ref(""),
  sort = ref("downloads"),
  repos = ref<Repo[]>([]),
  cursor = ref(""),
  loading = ref(false),
  error = ref("");
const chosen = ref(""),
  revision = ref(""),
  files = ref<File[]>([]),
  filename = ref(""),
  filesLoading = ref(false),
  fileError = ref(""),
  installing = ref(false);
const selectedFile = computed(() =>
  files.value.find((file) => file.filename === filename.value),
);
let generation = 0,
  fileGeneration = 0,
  timer: ReturnType<typeof setTimeout>;
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
  const request: Promise<Detail> = command("hub.files", { repo })
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
  // Keep results immediately usable; three metadata readers update sizes progressively.
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
    const result = await command("hub.search", {
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
watch(
  [query, sort],
  () => {
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
    clearTimeout(timer);
    timer = setTimeout(() => search(), 350);
  },
  { immediate: true },
);
async function choose(repo: string) {
  const id = ++fileGeneration;
  chosen.value = repo;
  files.value = [];
  filename.value = "";
  revision.value = "";
  fileError.value = "";
  filesLoading.value = true;
  await nextTick();
  selection.value?.scrollIntoView({ behavior: "smooth", block: "nearest" });
  try {
    const result = await detail(repo);
    if (id !== fileGeneration) return;
    revision.value = result.revision;
    files.value = result.files;
    filename.value =
      result.files.find(
        (f: File) => f.supported && /Q4_K_M/i.test(f.quantization),
      )?.filename ||
      result.files.find((f: File) => f.supported)?.filename ||
      "";
  } catch (e) {
    if (id === fileGeneration) fileError.value = String(e);
  } finally {
    if (id === fileGeneration) {
      filesLoading.value = false;
      await nextTick();
      selection.value?.scrollIntoView({ behavior: "smooth", block: "center" });
    }
  }
}
async function install() {
  if (!props.ready || props.disabled || !filename.value || installing.value)
    return;
  installing.value = true;
  fileError.value = "";
  try {
    await command("hub.install", {
      repo: chosen.value,
      filename: filename.value,
      revision: revision.value,
      download: true,
    });
    emit("installed");
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
  <div class="hub-browser">
    <div class="list-toolbar">
      <input
        v-model="query"
        type="search"
        aria-label="搜索 Hugging Face 模型"
        placeholder="搜索 Hugging Face，例如 Qwen、Llama…"
      />
    </div>
    <div class="hub-summary">
      <span>Hugging Face · {{ repos.length }} 个搜索结果</span
      ><label
        >排序
        <select v-model="sort" aria-label="社区模型排序">
          <option value="downloads">下载最多</option>
          <option value="updated">最近更新</option>
          <option value="likes">点赞最多</option>
        </select></label
      >
    </div>
    <p class="footnote">
      实时检索 Hugging Face 的 GGUF
      模型。模型大小随量化版本变化。选定版本后，文件直接下载到远端
      GPU；分片会自动一起下载。
    </p>
    <div v-if="error" class="inline-note" role="alert">
      {{ error }}<button class="text-button" @click="search()">重试搜索</button>
    </div>
    <p v-if="loading && !repos.length" role="status" class="empty-small">
      正在搜索模型社区…
    </p>
    <p v-else-if="!loading && !error && !repos.length" class="empty-small">
      没有找到匹配的 GGUF 模型，试试其他关键词。
    </p>
    <div class="hub-results">
      <article
        v-for="repo in repos"
        :key="repo.repo"
        class="hub-repo"
        :class="{ selected: chosen === repo.repo }"
      >
        <div>
          <h3>{{ repo.repo }}</h3>
          <div class="hub-facts">
            <span class="hub-size"
              ><small>模型大小</small
              ><strong v-if="repo.sizeState === 'ready'"
                >{{ size(repo.minSize!)
                }}<template v-if="repo.maxSize !== repo.minSize">
                  – {{ size(repo.maxSize!) }}</template
                ></strong
              ><strong v-else>{{
                repo.sizeState === "unavailable" ? "暂未提供" : "读取中…"
              }}</strong></span
            >
            <span
              ><small>下载量</small
              ><strong>{{ repo.downloads.toLocaleString() }}</strong></span
            >
            <span
              ><small>点赞</small
              ><strong>{{ repo.likes.toLocaleString() }}</strong></span
            >
          </div>
          <p class="hub-variant-note">
            <template v-if="repo.versions"
              >{{ repo.versions }} 个可选量化版本 · 大小随版本变化</template
            ><template v-else>选择版本后查看文件详情</template
            ><span v-if="repo.gated"> · 需要仓库授权</span>
          </p>
        </div>
        <button class="secondary" @click="choose(repo.repo)">选择版本</button>
      </article>
    </div>
    <button
      v-if="cursor"
      class="secondary hub-more"
      :disabled="loading"
      @click="search(true)"
    >
      {{ loading ? "加载中…" : "加载更多" }}
    </button>
    <section
      v-if="chosen"
      ref="selection"
      class="hub-selection"
      aria-label="社区模型版本"
    >
      <div class="hub-selection-heading">
        <h2>{{ chosen }}</h2>
        <button
          class="text-button"
          @click="openURL('https://huggingface.co/' + chosen)"
        >
          仓库详情 ↗
        </button>
      </div>
      <p v-if="filesLoading" role="status">正在读取量化版本与分片清单…</p>
      <div v-else-if="fileError" role="alert">
        {{ fileError
        }}<button class="text-button" @click="choose(chosen)">重新读取</button>
      </div>
      <template v-else>
        <label v-if="files.length"
          >量化版本<select v-model="filename" aria-label="选择量化版本">
            <option
              v-for="file in files"
              :key="file.filename"
              :value="file.filename"
              :disabled="!file.supported"
            >
              {{ file.filename }} · {{ size(file.size) }} ·
              {{ file.shards }} 个文件{{
                file.reason ? " · " + file.reason : ""
              }}
            </option>
          </select></label
        >
        <p v-else>此仓库没有可下载的 GGUF 文件。</p>
        <p v-if="selectedFile" class="hub-download-summary">
          下载大小 <strong>{{ size(selectedFile.size) }}</strong>
          <span>共 {{ selectedFile.shards }} 个文件 · 全部保存到远端 GPU</span>
        </p>
        <p class="footnote">
          请按显存和磁盘容量选择。带视觉组件的模型目前仅支持文本聊天；访问受限的仓库需先在社区获得权限，并在设置中保存
          Token。
        </p>
        <button
          v-if="ready"
          class="primary"
          :disabled="disabled || installing || !filename"
          @click="install"
        >
          {{ installing ? "正在提交…" : "添加并下载到远端" }}
        </button>
        <button v-else class="primary" @click="emit('device')">
          先启动 GPU
        </button>
      </template>
    </section>
  </div>
</template>
