import { t, tb } from "./i18n";
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
  return t("models.kindTextChat");
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
      label: t("models.badgeLoadedVram"),
      tone: "green",
      detail: t("models.badgeLoadedVramDetail"),
    };
  }
  if (running?.kind === "load") {
    return {
      state: "loading",
      label: t("models.badgeLoadingVram"),
      tone: "amber",
      detail: tb(running.message) || t("models.badgeLoadingVramDetail"),
    };
  }
  if (running?.kind === "download") {
    const pct = Math.round((running.progress || 0) * 100);
    return {
      state: "downloading",
      label: t("models.badgeRemoteDownloading", { pct }),
      tone: "amber",
      detail: tb(running.message) || t("models.badgeRemoteDownloadingDetail"),
      progress: running.progress || 0,
    };
  }
  if (runtime.state !== "stopped" && model.downloaded) {
    return {
      state: "downloaded",
      label: t("models.badgeRemoteDownloaded"),
      tone: "blue",
      detail: t("models.badgeRemoteDownloadedDetail"),
    };
  }
  if (runtime.state === "ready") {
    return {
      state: "remote_unfetched",
      label: t("models.badgeNotDownloaded"),
      tone: "muted",
      detail: t("models.badgeNotDownloadedDetail"),
    };
  }
  return {
    state: "offline",
    label: model.downloaded
      ? t("models.badgeRemoteDownloaded")
      : t("models.badgeWaitGpu"),
    tone: model.downloaded ? "blue" : "muted",
    detail: t("models.badgeWaitGpuDetail"),
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
