import type { ColabOneOutputShareGroupInput } from "./colabOneOutputShare";
import type { WorkflowOutput } from "./types";

const LEGACY_NON_COMMERCIAL_WORKFLOW_NAME_MAP: Readonly<
  Record<string, string>
> = {
  "AI 模特换装棚拍（仅非商用）": "AI 模特换装棚拍",
};

export function normalizeColabOneWorkflowDisplayName(
  name?: string | null,
): string {
  const trimmed = typeof name === "string" ? name.trim() : "";
  if (!trimmed) {
    return "";
  }
  return LEGACY_NON_COMMERCIAL_WORKFLOW_NAME_MAP[trimmed] || trimmed;
}

const COLABONE_INSPECT_FILENAME_RE =
  /(?:^|[\\/])(?:00_抠衣质检_|rmbg_inspect_|00_inspect_)|抠衣质检|_inspect_/i;

export interface ColabOneInspectableOutput {
  filename?: string;
  is_inspect?: boolean;
}

export interface ColabOneSortableGalleryItem extends ColabOneInspectableOutput {
  id?: string;
  created_at?: number;
}

export interface ColabOneOutputGroup {
  id: string;
  taskId: string;
  isAlbum: boolean;
  title: string;
  workflowId: string;
  workflowName: string;
  items: WorkflowOutput[];
  primaryCount: number;
  inspectCount: number;
  coverItems: WorkflowOutput[];
  extraCount: number;
  totalSize: number;
  totalElapsed: number;
  createdAt: number;
  allDriveSynced: boolean;
  anyDriveSynced: boolean;
  allExportedToDrive: boolean;
  hasImage: boolean;
  hasVideo: boolean;
  hasAudio: boolean;
}

/**
 * Returns true when an output artifact is an auxiliary inspection asset (such as
 * RMBG garment cutout previews) rather than a primary finished deliverable.
 */
export function isColabOneInspectOutput(
  item: ColabOneInspectableOutput | null | undefined,
): boolean {
  if (!item) {
    return false;
  }
  if (typeof item.is_inspect === "boolean") {
    return item.is_inspect;
  }
  return (
    typeof item.filename === "string" &&
    COLABONE_INSPECT_FILENAME_RE.test(item.filename)
  );
}

function compareColabOneOutputFilename(
  a: ColabOneSortableGalleryItem,
  b: ColabOneSortableGalleryItem,
): number {
  const nameA = typeof a.filename === "string" ? a.filename.trim() : "";
  const nameB = typeof b.filename === "string" ? b.filename.trim() : "";
  const cmp = nameA.localeCompare(nameB, undefined, {
    numeric: true,
    sensitivity: "base",
  });
  if (cmp !== 0) {
    return cmp;
  }
  const createdA =
    typeof a.created_at === "number" && Number.isFinite(a.created_at)
      ? a.created_at
      : 0;
  const createdB =
    typeof b.created_at === "number" && Number.isFinite(b.created_at)
      ? b.created_at
      : 0;
  if (createdA !== createdB) {
    return createdA - createdB;
  }
  return (a.id || "").localeCompare(b.id || "");
}

/**
 * Sorts gallery items with primary deliverables first (natural ascending
 * filename order) and auxiliary inspect images last (natural ascending
 * filename order).
 */
export function sortColabOneGalleryItems<T extends ColabOneSortableGalleryItem>(
  items: readonly T[],
): T[] {
  const primary: T[] = [];
  const inspect: T[] = [];
  for (const item of items) {
    if (!item) continue;
    if (isColabOneInspectOutput(item)) {
      inspect.push(item);
    } else {
      primary.push(item);
    }
  }
  primary.sort(compareColabOneOutputFilename);
  inspect.sort(compareColabOneOutputFilename);
  return [...primary, ...inspect];
}

/**
 * Picks up to `maxCount` cover thumbnails for an album, prioritizing primary
 * finished outputs and falling back to inspect outputs only when no primary
 * items exist.
 */
export function resolveColabOneCoverItems<T extends ColabOneInspectableOutput>(
  items: readonly T[],
  maxCount = 4,
): T[] {
  const limit = Math.max(0, Math.floor(maxCount));
  if (limit === 0 || items.length === 0) {
    return [];
  }
  const primary = items.filter((item) => !isColabOneInspectOutput(item));
  const source = primary.length > 0 ? primary : items;
  return source.slice(0, limit);
}

function normalizePositiveNumber(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) && value > 0
    ? value
    : 0;
}

function computeGroupTotalElapsed(items: readonly WorkflowOutput[]): number {
  const byPromptId = new Map<string, number>();
  let unpromptedSum = 0;
  for (const item of items) {
    const elapsed = normalizePositiveNumber(item.elapsed);
    if (elapsed <= 0) continue;
    const promptId =
      typeof item.prompt_id === "string" ? item.prompt_id.trim() : "";
    if (!promptId) {
      unpromptedSum += elapsed;
      continue;
    }
    const existing = byPromptId.get(promptId) ?? 0;
    if (elapsed > existing) {
      byPromptId.set(promptId, elapsed);
    }
  }
  let total = unpromptedSum;
  for (const val of byPromptId.values()) {
    total += val;
  }
  return total;
}

function isDriveSyncedOutput(item: WorkflowOutput): boolean {
  return Boolean(item.drive_synced || item.storage === "drive");
}

function buildOutputGroup(
  rawItems: WorkflowOutput[],
  isAlbum: boolean,
  taskId: string,
): ColabOneOutputGroup {
  const items = isAlbum ? sortColabOneGalleryItems(rawItems) : [...rawItems];
  const primaryItems = items.filter((item) => !isColabOneInspectOutput(item));
  const inspectCount = items.length - primaryItems.length;
  const primaryCount = primaryItems.length;
  const coverItems = resolveColabOneCoverItems(items, 4);
  const extraCount = Math.max(0, items.length - coverItems.length);

  const workflowId =
    items.find((it) => typeof it.workflow_id === "string" && it.workflow_id.trim())
      ?.workflow_id.trim() || "";
  const workflowName = normalizeColabOneWorkflowDisplayName(
    items.find(
      (it) => typeof it.workflow_name === "string" && it.workflow_name.trim(),
    )?.workflow_name,
  );
  const fallbackName = items[0]?.filename?.trim() || taskId || items[0]?.id || "";
  const title = workflowName || fallbackName;

  let totalSize = 0;
  let createdAt = 0;
  let hasImage = false;
  let hasVideo = false;
  let hasAudio = false;
  let allDriveSynced = items.length > 0;
  let anyDriveSynced = false;
  let allExportedToDrive = items.length > 0;

  for (const item of items) {
    totalSize += normalizePositiveNumber(item.size);
    const created = normalizePositiveNumber(item.created_at);
    if (created > createdAt) {
      createdAt = created;
    }
    if (item.kind === "image") hasImage = true;
    else if (item.kind === "video") hasVideo = true;
    else if (item.kind === "audio") hasAudio = true;

    const synced = isDriveSyncedOutput(item);
    if (synced) {
      anyDriveSynced = true;
    } else {
      allDriveSynced = false;
    }
    if (!item.drive_exported) {
      allExportedToDrive = false;
    }
  }

  return {
    id: isAlbum ? `album:${taskId}` : `item:${items[0]?.id || ""}`,
    taskId,
    isAlbum,
    title,
    workflowId,
    workflowName,
    items,
    primaryCount,
    inspectCount,
    coverItems,
    extraCount,
    totalSize,
    totalElapsed: computeGroupTotalElapsed(items),
    createdAt,
    allDriveSynced,
    anyDriveSynced,
    allExportedToDrive,
    hasImage,
    hasVideo,
    hasAudio,
  };
}

/**
 * Groups ComfyUI outputs into multi-item albums when two or more outputs share
 * the same non-empty `task_id`, keeping single outputs as standalone cards.
 * Groups are returned newest-first by `createdAt`.
 */
export function groupColabOneOutputs(
  outputs: readonly WorkflowOutput[],
): ColabOneOutputGroup[] {
  if (!Array.isArray(outputs) || outputs.length === 0) {
    return [];
  }

  const taskBuckets = new Map<string, WorkflowOutput[]>();
  const orderedEntries: Array<
    | { kind: "task"; taskId: string; order: number }
    | { kind: "single"; item: WorkflowOutput; order: number }
  > = [];

  for (let i = 0; i < outputs.length; i += 1) {
    const item = outputs[i];
    if (!item || !item.id) continue;
    const taskId = typeof item.task_id === "string" ? item.task_id.trim() : "";
    if (!taskId) {
      orderedEntries.push({ kind: "single", item, order: i });
      continue;
    }
    const existing = taskBuckets.get(taskId);
    if (existing) {
      existing.push(item);
    } else {
      taskBuckets.set(taskId, [item]);
      orderedEntries.push({ kind: "task", taskId, order: i });
    }
  }

  const indexedGroups: Array<{ group: ColabOneOutputGroup; order: number }> = [];
  for (const entry of orderedEntries) {
    if (entry.kind === "single") {
      indexedGroups.push({
        group: buildOutputGroup([entry.item], false, ""),
        order: entry.order,
      });
      continue;
    }
    const bucket = taskBuckets.get(entry.taskId) ?? [];
    if (bucket.length >= 2) {
      indexedGroups.push({
        group: buildOutputGroup(bucket, true, entry.taskId),
        order: entry.order,
      });
    } else if (bucket.length === 1) {
      indexedGroups.push({
        group: buildOutputGroup(bucket, false, entry.taskId),
        order: entry.order,
      });
    }
  }

  indexedGroups.sort(
    (a, b) => b.group.createdAt - a.group.createdAt || a.order - b.order,
  );
  return indexedGroups.map((entry) => entry.group);
}

export interface ColabOneGroupCUSummary {
  total: number;
  hasAny: boolean;
  anyEstimated: boolean;
  sample: WorkflowOutput | null;
}

/**
 * Aggregates compute-unit (CU) usage across a group's outputs while
 * deduplicating shared `prompt_id` batches (taking the max CU per prompt_id
 * rather than multiplying identical batch prompt CU by the output count).
 */
function scoreColabOneCUSampleMetadata(item: WorkflowOutput | null): number {
  if (!item) return -1;
  let score = 0;
  if (typeof item.gpu_name === "string" && item.gpu_name.trim()) score += 2;
  if (normalizePositiveNumber(item.cu_rate) > 0) score += 1;
  return score;
}

export function computeColabOneGroupCU(
  items: readonly WorkflowOutput[],
): ColabOneGroupCUSummary {
  const byPromptId = new Map<string, number>();
  let unpromptedSum = 0;
  let hasAny = false;
  let anyEstimated = false;
  let sample: WorkflowOutput | null = null;

  for (const item of items) {
    const cu = normalizePositiveNumber(item?.cu_consumed);
    if (cu <= 0) continue;
    hasAny = true;
    if (
      !sample ||
      scoreColabOneCUSampleMetadata(item) > scoreColabOneCUSampleMetadata(sample)
    ) {
      sample = item;
    }
    if (item.cu_estimated) {
      anyEstimated = true;
    }
    const promptId =
      typeof item.prompt_id === "string" ? item.prompt_id.trim() : "";
    if (!promptId) {
      unpromptedSum += cu;
      continue;
    }
    const existing = byPromptId.get(promptId) ?? 0;
    if (cu > existing) {
      byPromptId.set(promptId, cu);
    }
  }

  let total = unpromptedSum;
  for (const val of byPromptId.values()) {
    total += val;
  }
  return { total, hasAny, anyEstimated, sample };
}

export function sanitizeColabOneShareGroupId(
  raw: string,
  fallbackIdx: number,
): string {
  const cleaned = String(raw || "")
    .trim()
    .replace(/[^a-zA-Z0-9._:-]+/g, "_")
    .slice(0, 80);
  return cleaned || `group-${fallbackIdx + 1}`;
}

export const sanitizeShareGroupId = sanitizeColabOneShareGroupId;

/**
 * Converts host group selections into ordered share creation group inputs,
 * dropping empty groups and preserving natural item ordering inside each group.
 */
export function buildColabOneOutputShareGroupInputs(
  groups: readonly ColabOneOutputGroup[],
  selectedIdsByGroup: Record<string, readonly string[] | undefined>,
): ColabOneOutputShareGroupInput[] {
  if (!groups || groups.length === 0 || !selectedIdsByGroup) {
    return [];
  }

  const result: ColabOneOutputShareGroupInput[] = [];
  for (let idx = 0; idx < groups.length; idx += 1) {
    const group = groups[idx];
    const rawSelected = selectedIdsByGroup[group.id];
    if (!rawSelected || rawSelected.length === 0) {
      continue;
    }
    const selectedSet = new Set<string>();
    for (const rawId of rawSelected) {
      if (typeof rawId !== "string") continue;
      const trimmed = rawId.trim();
      if (trimmed) {
        selectedSet.add(trimmed);
      }
    }
    if (selectedSet.size === 0) {
      continue;
    }
    const orderedOutputIds = group.items
      .map((item) => item.id)
      .filter((id) => selectedSet.has(id));
    if (orderedOutputIds.length === 0) {
      continue;
    }
    result.push({
      id: sanitizeColabOneShareGroupId(group.id, idx),
      title: group.title,
      workflow_id: group.workflowId,
      workflow_name: group.workflowName,
      task_id: group.taskId,
      output_ids: orderedOutputIds,
    });
  }
  return result;
}
