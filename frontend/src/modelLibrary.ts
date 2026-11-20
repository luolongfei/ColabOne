import type { ModelSpec, RuntimeStatus } from "./types";

export type ModelLibraryTab = "common" | "hub";

export interface ModelRemoteBadge {
  state:
    | "loaded"
    | "loading"
    | "downloading"
    | "downloaded"
    | "remote_unfetched"
    | "offline";
  label: string;
  tone: "green" | "blue" | "amber" | "muted";
  detail: string;
  progress?: number;
}

export function normalizeModelTab(tab?: string | null): ModelLibraryTab {
  return tab === "hub" ? "hub" : "common";
}

export function isModelActiveInVram(
  model: ModelSpec,
  runtime: RuntimeStatus,
): boolean {
  return (
    runtime.state !== "stopped" &&
    !!(model.loaded || (!!model.id && runtime.loaded_model === model.id))
  );
}

export function modelKindLabel(_kind?: ModelSpec["kind"]): string {
  return "文本对话";
}

function modelRepo(model: ModelSpec): string {
  return model.repo || model.files?.[0]?.repo || "";
}

function isModelTaskActive(model: ModelSpec, runtime: RuntimeStatus): boolean {
  return !!runtime.tasks?.some(
    (t) =>
      t.state === "running" &&
      t.model_id === model.id &&
      (t.kind === "download" || t.kind === "load"),
  );
}

function matchesModelQuery(model: ModelSpec, normalizedQuery: string): boolean {
  if (!normalizedQuery) return true;
  const haystack = [
    model.name || "",
    model.description || "",
    modelRepo(model),
    modelKindLabel(model.kind),
  ]
    .join(" ")
    .toLowerCase();
  return haystack.includes(normalizedQuery);
}

export function filterCommonModels(
  models: ModelSpec[],
  query: string,
  runtime: RuntimeStatus,
): ModelSpec[] {
  const q = (query || "").trim().toLowerCase();
  const included = models.filter(
    (m) =>
      (!!m.common ||
        m.downloaded ||
        isModelActiveInVram(m, runtime) ||
        isModelTaskActive(m, runtime)) &&
      matchesModelQuery(m, q),
  );
  return [...included].sort((a, b) => {
    const activeDiff =
      Number(isModelActiveInVram(b, runtime)) -
      Number(isModelActiveInVram(a, runtime));
    if (activeDiff !== 0) return activeDiff;

    const taskDiff =
      Number(isModelTaskActive(b, runtime)) -
      Number(isModelTaskActive(a, runtime));
    if (taskDiff !== 0) return taskDiff;

    const downloadedDiff = Number(!!b.downloaded) - Number(!!a.downloaded);
    if (downloadedDiff !== 0) return downloadedDiff;

    const commonDiff = Number(!!b.common) - Number(!!a.common);
    if (commonDiff !== 0) return commonDiff;

    return 0;
  });
}

export function filterCuratedCommunityModels(
  models: ModelSpec[],
  query: string,
): ModelSpec[] {
  const q = (query || "").trim().toLowerCase();
  return models.filter((m) => !m.custom && matchesModelQuery(m, q));
}

export function describeModelRemoteStatus(
  model: ModelSpec,
  runtime: RuntimeStatus,
): ModelRemoteBadge {
  const running = runtime.tasks?.find(
    (t) =>
      t.state === "running" &&
      t.model_id === model.id &&
      (t.kind === "load" || t.kind === "download"),
  );
  if (isModelActiveInVram(model, runtime)) {
    return {
      state: "loaded",
      label: "已装载显存",
      tone: "green",
      detail: "当前已在远端 GPU 显存就绪，可通过对外 API 调用",
    };
  }
  if (running?.kind === "load") {
    return {
      state: "loading",
      label: "正在装载显存…",
      tone: "amber",
      detail: running.message || "正在装载到远端 GPU 显存",
    };
  }
  if (running?.kind === "download") {
    const pct = Math.round((running.progress || 0) * 100);
    return {
      state: "downloading",
      label: "远端下载中 " + pct + "%",
      tone: "amber",
      detail: running.message || "正在高速下载到远端磁盘",
      progress: running.progress || 0,
    };
  }
  if (runtime.state !== "stopped" && model.downloaded) {
    return {
      state: "downloaded",
      label: "已下载到远端",
      tone: "blue",
      detail: "权重已缓存在当前远端磁盘，可一键装载到显存",
    };
  }
  if (runtime.state === "ready") {
    return {
      state: "remote_unfetched",
      label: "未下载到远端",
      tone: "muted",
      detail: "远端磁盘尚未缓存此模型，可一键下载并装载",
    };
  }
  return {
    state: "offline",
    label: model.downloaded ? "已下载到远端" : "待启动远端 GPU",
    tone: model.downloaded ? "blue" : "muted",
    detail: "启动远端 GPU 后可查看远端缓存或一键下载装载",
  };
}

export function repoRemoteSummary(
  repo: string,
  models: ModelSpec[],
  runtime: RuntimeStatus,
): {
  common: boolean;
  downloadedCount: number;
  loadedName?: string;
  savedModels: ModelSpec[];
} {
  const target = (repo || "").trim().toLowerCase();
  const savedModels = target
    ? models.filter((m) => modelRepo(m).trim().toLowerCase() === target)
    : [];
  const loadedModel = savedModels.find((m) => isModelActiveInVram(m, runtime));
  return {
    common: savedModels.some((m) => !!m.common),
    downloadedCount: savedModels.filter((m) => !!m.downloaded).length,
    ...(loadedModel ? { loadedName: loadedModel.name } : {}),
    savedModels,
  };
}
