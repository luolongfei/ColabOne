import { colabOneCommand } from "./api";
import { colabOneWebBackendReady } from "./colabOneWebConsole";
import {
  colabOneWorkflowShareId,
  isColabOneWorkflowShareActive,
} from "./colabOneWorkflowShare";
import {
  COLABONE_VOICE_PRESET_ID_RE,
  clampColabOneNumericFieldValue,
  defaultFieldValues,
  isColabOnePresetVisualPrompt,
} from "./comfyui";
import { t, type ColabOneLocale } from "./i18n";
import { COLABONE_STORAGE_KEYS } from "./storage";
import type { WorkflowField } from "./types";

export interface ColabOneWorkflowDraftMediaMeta {
  id: string;
  filename: string;
  kind: "image" | "audio";
  mime: string;
  size: number;
  sha256: string;
  updated_at: number;
}

export interface ColabOneWorkflowDraftMediaPayload
  extends ColabOneWorkflowDraftMediaMeta {
  data: string;
}

export interface ColabOneWorkflowDraft {
  workflow_id: string;
  fields: Record<string, string | number | boolean>;
  media: Record<string, ColabOneWorkflowDraftMediaMeta>;
  updated_at: number;
}

export interface ColabOneWorkflowDraftMediaSaveInput {
  filename: string;
  kind: "image" | "audio";
  data: string;
}

export type ColabOneDraftCommandInvoker = (
  action: string,
  payload?: Record<string, unknown>,
) => Promise<any>;

export interface ColabOneWorkflowDraftClientOptions {
  allowLocalFallback?: boolean;
}

export interface ColabOneWorkflowDraftClient {
  loadColabOneWorkflowDraft(
    workflowId: string,
  ): Promise<ColabOneWorkflowDraft | null>;
  saveColabOneWorkflowDraft(
    workflowId: string,
    fields: Record<string, string | number | boolean>,
  ): Promise<ColabOneWorkflowDraft | null>;
  saveColabOneWorkflowDraftMedia(
    workflowId: string,
    fieldKey: string,
    payload: ColabOneWorkflowDraftMediaSaveInput,
  ): Promise<ColabOneWorkflowDraftMediaMeta | null>;
  readColabOneWorkflowDraftMedia(
    workflowId: string,
    fieldKey: string,
  ): Promise<ColabOneWorkflowDraftMediaPayload | null>;
  deleteColabOneWorkflowDraftMedia(
    workflowId: string,
    fieldKey: string,
  ): Promise<void>;
  clearColabOneWorkflowDraft(workflowId: string): Promise<void>;
}

interface StoredLocalDraftRecord {
  workflow_id: string;
  fields: Record<string, string | number | boolean>;
  updated_at: number;
}

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasOwn(
  record: Record<string, unknown> | undefined | null,
  key: string,
): boolean {
  return Boolean(record) && Object.prototype.hasOwnProperty.call(record, key);
}

function normalizeZero(num: number): number {
  return num === 0 ? 0 : num;
}

function isPrimitiveFieldValue(
  value: unknown,
): value is string | number | boolean {
  if (typeof value === "string" || typeof value === "boolean") {
    return true;
  }
  return typeof value === "number" && Number.isFinite(value);
}

function isForbiddenObjectKey(key: string): boolean {
  return key === "__proto__" || key === "constructor" || key === "prototype";
}

function sanitizePrimitiveRecord(
  record: unknown,
): Record<string, string | number | boolean> {
  const sanitized: Record<string, string | number | boolean> = {};
  if (!isPlainRecord(record)) {
    return sanitized;
  }
  for (const [key, value] of Object.entries(record)) {
    if (!key || isForbiddenObjectKey(key)) continue;
    if (typeof value === "string" || typeof value === "boolean") {
      sanitized[key] = value;
    } else if (typeof value === "number" && Number.isFinite(value)) {
      sanitized[key] = normalizeZero(value);
    }
  }
  return sanitized;
}

function isMyGoBridgePresent(): boolean {
  return (
    (typeof globalThis === "object" &&
      (globalThis as { mygo?: unknown }).mygo !== undefined) ||
    colabOneWebBackendReady.value
  );
}

function isBridgeUnavailableError(err: unknown): boolean {
  if (!err) {
    return false;
  }
  const message =
    err instanceof Error
      ? err.message
      : typeof err === "string"
        ? err
        : typeof (err as { message?: unknown }).message === "string"
          ? String((err as { message?: unknown }).message)
          : "";
  return /mygo:\s*runtime not found|bridge unavailable/i.test(message);
}

function normalizeMediaMeta(
  raw: unknown,
): ColabOneWorkflowDraftMediaMeta | null {
  if (!isPlainRecord(raw)) {
    return null;
  }
  const kind = raw.kind === "image" || raw.kind === "audio" ? raw.kind : null;
  const filename = typeof raw.filename === "string" ? raw.filename : "";
  if (!kind || !filename) {
    return null;
  }
  return {
    id: typeof raw.id === "string" ? raw.id : "",
    filename,
    kind,
    mime: typeof raw.mime === "string" ? raw.mime : "",
    size:
      typeof raw.size === "number" && Number.isFinite(raw.size) && raw.size >= 0
        ? raw.size
        : 0,
    sha256: typeof raw.sha256 === "string" ? raw.sha256 : "",
    updated_at:
      typeof raw.updated_at === "number" && Number.isFinite(raw.updated_at)
        ? raw.updated_at
        : 0,
  };
}

function normalizeMediaPayload(
  raw: unknown,
): ColabOneWorkflowDraftMediaPayload | null {
  if (!isPlainRecord(raw) || typeof raw.data !== "string") {
    return null;
  }
  const meta = normalizeMediaMeta(raw);
  if (!meta) {
    return null;
  }
  return {
    ...meta,
    data: raw.data,
  };
}

function normalizeDraft(
  raw: unknown,
  fallbackWorkflowId: string,
): ColabOneWorkflowDraft | null {
  if (!isPlainRecord(raw)) {
    return null;
  }
  const workflowId =
    typeof raw.workflow_id === "string" && raw.workflow_id.trim()
      ? raw.workflow_id.trim()
      : fallbackWorkflowId;
  if (!workflowId) {
    return null;
  }
  const fields = sanitizePrimitiveRecord(raw.fields);
  const media: Record<string, ColabOneWorkflowDraftMediaMeta> = {};
  if (isPlainRecord(raw.media)) {
    for (const [key, val] of Object.entries(raw.media)) {
      if (!key || isForbiddenObjectKey(key)) continue;
      const normalized = normalizeMediaMeta(val);
      if (normalized) {
        media[key] = normalized;
      }
    }
  }
  const updatedAt =
    typeof raw.updated_at === "number" && Number.isFinite(raw.updated_at)
      ? raw.updated_at
      : 0;
  return {
    workflow_id: workflowId,
    fields,
    media,
    updated_at: updatedAt,
  };
}

function resolveLocalDraftStorageKey(
  shareMode = isColabOneWorkflowShareActive(),
): string | null {
  if (shareMode) {
    const shareId = (colabOneWorkflowShareId.value || "").trim();
    if (!shareId) {
      return null;
    }
    return `${COLABONE_STORAGE_KEYS.workflowShareDraftsPrefix}${shareId}`;
  }
  return COLABONE_STORAGE_KEYS.comfyDrafts;
}

function captureDraftContext(): {
  shareMode: boolean;
  storageKey: string | null;
} {
  const shareMode = isColabOneWorkflowShareActive();
  return {
    shareMode,
    storageKey: resolveLocalDraftStorageKey(shareMode),
  };
}

function readLocalDraftMap(
  storage: Storage | null,
  storageKey: string | null = resolveLocalDraftStorageKey(),
): Record<string, StoredLocalDraftRecord> {
  if (!storage || !storageKey) {
    return {};
  }
  try {
    const raw = storage.getItem(storageKey);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw);
    if (!isPlainRecord(parsed)) {
      return {};
    }
    const result: Record<string, StoredLocalDraftRecord> = {};
    for (const [wfId, entry] of Object.entries(parsed)) {
      if (!wfId || isForbiddenObjectKey(wfId) || !isPlainRecord(entry)) {
        continue;
      }
      result[wfId] = {
        workflow_id:
          typeof entry.workflow_id === "string" && entry.workflow_id.trim()
            ? entry.workflow_id.trim()
            : wfId,
        fields: sanitizePrimitiveRecord(entry.fields),
        updated_at:
          typeof entry.updated_at === "number" &&
          Number.isFinite(entry.updated_at)
            ? entry.updated_at
            : 0,
      };
    }
    return result;
  } catch {
    return {};
  }
}

function writeLocalDraftMap(
  storage: Storage | null,
  map: Record<string, StoredLocalDraftRecord>,
  storageKey: string | null = resolveLocalDraftStorageKey(),
): void {
  if (!storage || !storageKey) {
    return;
  }
  try {
    const keys = Object.keys(map);
    if (keys.length === 0) {
      storage.removeItem(storageKey);
    } else {
      storage.setItem(storageKey, JSON.stringify(map));
    }
  } catch {
    // Ignore storage quota or restricted storage errors.
  }
}

function readLocalDraft(
  storage: Storage | null,
  workflowId: string,
  storageKey: string | null = resolveLocalDraftStorageKey(),
): ColabOneWorkflowDraft | null {
  if (!storageKey) {
    return null;
  }
  const map = readLocalDraftMap(storage, storageKey);
  if (!hasOwn(map, workflowId)) {
    return null;
  }
  const record = map[workflowId];
  return {
    workflow_id: record.workflow_id,
    fields: { ...record.fields },
    media: {},
    updated_at: record.updated_at,
  };
}

function writeLocalDraft(
  storage: Storage | null,
  workflowId: string,
  fields: Record<string, string | number | boolean>,
  updatedAt: number,
  storageKey: string | null = resolveLocalDraftStorageKey(),
): ColabOneWorkflowDraft {
  const safeFields = sanitizePrimitiveRecord(fields);
  const draft: ColabOneWorkflowDraft = {
    workflow_id: workflowId,
    fields: safeFields,
    media: {},
    updated_at: updatedAt,
  };
  if (storage && storageKey && !isForbiddenObjectKey(workflowId)) {
    const map = readLocalDraftMap(storage, storageKey);
    map[workflowId] = {
      workflow_id: workflowId,
      fields: safeFields,
      updated_at: updatedAt,
    };
    writeLocalDraftMap(storage, map, storageKey);
  }
  return draft;
}

function deleteLocalDraft(
  storage: Storage | null,
  workflowId: string,
  storageKey: string | null = resolveLocalDraftStorageKey(),
): void {
  if (!storage || !storageKey) {
    return;
  }
  const map = readLocalDraftMap(storage, storageKey);
  if (!hasOwn(map, workflowId)) {
    return;
  }
  delete map[workflowId];
  writeLocalDraftMap(storage, map, storageKey);
}

export function colabOneBase64ToBlob(base64: string, mime: string): Blob {
  const normalized = (base64 || "").replace(/\s+/g, "");
  const binary = atob(normalized);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return new Blob([bytes], {
    type: (mime || "").trim() || "application/octet-stream",
  });
}

const COLABONE_LEGACY_LONGCAT_DEFAULTS: Record<string, string | number> = {
  "pipeline.upscale_mode": "none",
  "pipeline.max_segment_seconds": 9,
  "14.steps": 12,
};

const COLABONE_LEGACY_LONGCAT_15_UPSCALE_MODES: ReadonlySet<string> = new Set([
  "ai_2x",
  "lanczos",
]);

function isLegacyLongCatDefaultValue(fieldKey: string, raw: unknown): boolean {
  if (!hasOwn(COLABONE_LEGACY_LONGCAT_DEFAULTS, fieldKey)) {
    return false;
  }
  const legacy = COLABONE_LEGACY_LONGCAT_DEFAULTS[fieldKey];
  if (typeof legacy === "number") {
    const parsed =
      typeof raw === "number"
        ? raw
        : typeof raw === "string" && raw.trim() !== ""
          ? Number(raw.trim())
          : NaN;
    return Number.isFinite(parsed) && parsed === legacy;
  }
  return typeof raw === "string" && raw.trim() === legacy;
}

export function mergeColabOneWorkflowDraftFields(
  fields: WorkflowField[],
  draftFields: Record<string, string | number | boolean> | undefined,
  locale?: ColabOneLocale,
  validMedia?: Record<string, ColabOneWorkflowDraftMediaMeta>,
): Record<string, string | number | boolean> {
  const merged = defaultFieldValues(fields, locale);
  const hasDraftFields =
    isPlainRecord(draftFields) && Object.keys(draftFields).length > 0;
  const targetHasRenderQuality = (fields || []).some(
    (field) => field.key === "pipeline.render_quality",
  );
  const targetHasQualityPreset = (fields || []).some(
    (field) => field.key === "pipeline.quality_preset",
  );
  const isPreOptimizationLongCatDraft =
    hasDraftFields &&
    targetHasRenderQuality &&
    !hasOwn(draftFields, "pipeline.render_quality");
  const rawDraftQualityPreset = hasOwn(draftFields, "pipeline.quality_preset")
    ? draftFields?.["pipeline.quality_preset"]
    : undefined;
  const hasQualityPresetInDraft =
    typeof rawDraftQualityPreset === "string" &&
    rawDraftQualityPreset.trim().length > 0;
  const rawDraftUpscaleMode = hasOwn(draftFields, "pipeline.upscale_mode")
    ? draftFields?.["pipeline.upscale_mode"]
    : undefined;
  const rawDraftUpscaleModeStr =
    typeof rawDraftUpscaleMode === "string" ? rawDraftUpscaleMode.trim() : "";
  const hasLegacyLongCat15UpscaleMode =
    COLABONE_LEGACY_LONGCAT_15_UPSCALE_MODES.has(rawDraftUpscaleModeStr);
  const isLegacyLongCat15Draft =
    hasDraftFields &&
    targetHasQualityPreset &&
    !hasQualityPresetInDraft &&
    (hasOwn(draftFields, "pipeline.render_quality") ||
      hasOwn(draftFields, "pipeline.acceleration_profile") ||
      hasOwn(draftFields, "14.steps") ||
      hasOwn(draftFields, "pipeline.max_segment_seconds") ||
      hasOwn(draftFields, "5.positive_prompt") ||
      hasOwn(draftFields, "15.seed") ||
      hasLegacyLongCat15UpscaleMode);
  const isPreOptimizationIntoLongCat16Draft =
    isLegacyLongCat15Draft &&
    !hasOwn(draftFields, "pipeline.render_quality") &&
    !hasOwn(draftFields, "pipeline.acceleration_profile");
  const preservesExplicitLongCat15None =
    isLegacyLongCat15Draft &&
    rawDraftUpscaleModeStr === "none" &&
    !isPreOptimizationIntoLongCat16Draft;

  for (const field of fields || []) {
    if (field.field_type === "image" || field.field_type === "audio") {
      // Never resurrect ghost media filenames from text fields or localStorage
      // unless backed by actual persisted draft media metadata.
      if (validMedia && hasOwn(validMedia, field.key)) {
        const meta = validMedia[field.key];
        if (meta && meta.filename) {
          merged[field.key] = meta.filename;
        }
      }
      continue;
    }

    if (!draftFields || !isPlainRecord(draftFields) || !hasOwn(draftFields, field.key)) {
      continue;
    }
    const raw: unknown = draftFields[field.key];
    if (raw === undefined || raw === null) {
      continue;
    }
    if (
      isPreOptimizationLongCatDraft &&
      isLegacyLongCatDefaultValue(field.key, raw)
    ) {
      continue;
    }
    if (isLegacyLongCat15Draft) {
      const rawStr = typeof raw === "string" ? raw.trim() : "";
      if (
        field.key === "pipeline.target_resolution" &&
        rawStr === "1280x720" &&
        !preservesExplicitLongCat15None
      ) {
        continue;
      }
      if (field.key === "pipeline.upscale_mode") {
        const hasExplicitOptions =
          Array.isArray(field.options) && field.options.length > 0;
        if (
          COLABONE_LEGACY_LONGCAT_15_UPSCALE_MODES.has(rawStr) &&
          (!hasExplicitOptions || !field.options!.includes(rawStr))
        ) {
          continue;
        }
        if (isPreOptimizationIntoLongCat16Draft && rawStr === "none") {
          continue;
        }
      }
    }

    if (field.field_type === "select") {
      if (
        typeof raw !== "string" &&
        !(typeof raw === "number" && Number.isFinite(raw))
      ) {
        continue;
      }
      const candidate = String(raw).trim();
      if (!candidate) {
        continue;
      }
      // `pipeline.voice_preset` dynamically accepts engine-specific official
      // speaker IDs (e.g. Qwen3-TTS `qwen_*`) outside the static template options.
      const allowsDynamicSelectOptions = field.key === "pipeline.voice_preset";
      if (allowsDynamicSelectOptions) {
        if (!COLABONE_VOICE_PRESET_ID_RE.test(candidate)) {
          continue;
        }
        merged[field.key] =
          candidate.toLowerCase() === "custom" ? "custom" : candidate;
        continue;
      }
      if (
        Array.isArray(field.options) &&
        field.options.length > 0 &&
        !field.options.includes(candidate)
      ) {
        continue;
      }
      merged[field.key] = candidate;
      continue;
    }

    if (field.field_type === "boolean") {
      if (typeof raw === "boolean") {
        merged[field.key] = raw;
      } else if (typeof raw === "string") {
        const normalized = raw.trim().toLowerCase();
        if (normalized === "true" || normalized === "1") {
          merged[field.key] = true;
        } else if (normalized === "false" || normalized === "0") {
          merged[field.key] = false;
        }
      }
      continue;
    }

    if (field.field_type === "integer" || field.field_type === "number") {
      if (typeof raw === "string" && raw.trim() === "") {
        merged[field.key] = "";
        continue;
      }
      let num: number | undefined;
      if (typeof raw === "number") {
        num = Number.isFinite(raw) ? raw : undefined;
      } else if (typeof raw === "string") {
        const parsed = Number(raw.trim());
        num = Number.isFinite(parsed) ? parsed : undefined;
      }
      if (num !== undefined) {
        merged[field.key] = clampColabOneNumericFieldValue(num, field);
      }
      continue;
    }

    if (field.field_type === "text" || field.field_type === "textarea") {
      if (typeof raw !== "string") {
        continue;
      }
      if (
        typeof field.default === "string" &&
        isColabOnePresetVisualPrompt(field.default) &&
        isColabOnePresetVisualPrompt(raw)
      ) {
        merged[field.key] = t("comfy.defaultVisualPrompt", undefined, locale);
      } else {
        merged[field.key] = raw;
      }
      continue;
    }

    if (isPrimitiveFieldValue(raw)) {
      merged[field.key] = String(raw);
    }
  }

  return merged;
}

export function hasColabOneDraftContent(
  fields: WorkflowField[],
  values: Record<string, string | number | boolean>,
  mediaKeys: string[],
  locale?: ColabOneLocale,
): boolean {
  if (Array.isArray(mediaKeys) && mediaKeys.length > 0) {
    return true;
  }
  const defaults = defaultFieldValues(fields, locale);
  for (const field of fields || []) {
    if (!hasOwn(values, field.key)) {
      continue;
    }
    const current = values[field.key];
    if (current === undefined || current === null) {
      continue;
    }
    const def = defaults[field.key];
    if (current === def) {
      continue;
    }
    if (
      typeof field.default === "string" &&
      isColabOnePresetVisualPrompt(field.default) &&
      isColabOnePresetVisualPrompt(current)
    ) {
      continue;
    }
    return true;
  }
  return false;
}

export function sanitizeColabOneDraftFieldsForSave(
  fields: WorkflowField[],
  values: Record<string, string | number | boolean>,
): Record<string, string | number | boolean> {
  const sanitized: Record<string, string | number | boolean> = {};
  for (const field of fields || []) {
    if (!hasOwn(values, field.key)) {
      continue;
    }
    const raw = values[field.key];
    if (typeof raw === "string" || typeof raw === "boolean") {
      sanitized[field.key] = raw;
    } else if (typeof raw === "number" && Number.isFinite(raw)) {
      sanitized[field.key] = normalizeZero(raw);
    }
  }
  return sanitized;
}

export function createColabOneWorkflowDraftClient(
  invoke?: ColabOneDraftCommandInvoker,
  storage?: Storage | null,
  options?: ColabOneWorkflowDraftClientOptions,
): ColabOneWorkflowDraftClient {
  const isDefaultInvoker = typeof invoke !== "function";
  const invoker: ColabOneDraftCommandInvoker = isDefaultInvoker
    ? (action, payload) => colabOneCommand(action, payload)
    : invoke;

  const writeQueues = new Map<string, Promise<void>>();

  function resolveStorage(): Storage | null {
    if (storage !== undefined) {
      return storage;
    }
    if (typeof window === "undefined") {
      return null;
    }
    try {
      return window.localStorage;
    } catch {
      return null;
    }
  }

  function canFallback(err: unknown): boolean {
    if (options?.allowLocalFallback !== undefined) {
      return options.allowLocalFallback;
    }
    if (isBridgeUnavailableError(err)) {
      return true;
    }
    return isDefaultInvoker && !isMyGoBridgePresent();
  }

  function enqueueWrite<T>(
    workflowId: string,
    task: () => Promise<T>,
  ): Promise<T> {
    const prev = writeQueues.get(workflowId);
    const run = prev
      ? prev.then(task, task)
      : (() => {
          try {
            return task();
          } catch (err) {
            return Promise.reject(err);
          }
        })();
    const settled = run.then(
      () => undefined,
      () => undefined,
    );
    writeQueues.set(workflowId, settled);
    void settled.finally(() => {
      if (writeQueues.get(workflowId) === settled) {
        writeQueues.delete(workflowId);
      }
    });
    return run;
  }

  async function waitForPendingWrites(workflowId: string): Promise<void> {
    while (true) {
      const pending = writeQueues.get(workflowId);
      if (!pending) {
        return;
      }
      await pending;
      if (writeQueues.get(workflowId) === pending) {
        return;
      }
    }
  }

  async function loadColabOneWorkflowDraft(
    workflowId: string,
  ): Promise<ColabOneWorkflowDraft | null> {
    const wfId = (workflowId || "").trim();
    if (!wfId) {
      return null;
    }
    const { shareMode, storageKey } = captureDraftContext();
    await waitForPendingWrites(wfId);
    if (shareMode) {
      if (!storageKey) {
        return null;
      }
      return readLocalDraft(resolveStorage(), wfId, storageKey);
    }
    try {
      const res = await invoker("comfyui.draft.get", { workflow_id: wfId });
      if (!res || res.draft === null || res.draft === undefined) {
        return null;
      }
      return normalizeDraft(res.draft, wfId);
    } catch (err) {
      if (canFallback(err)) {
        return readLocalDraft(resolveStorage(), wfId, storageKey);
      }
      throw err;
    }
  }

  function saveColabOneWorkflowDraft(
    workflowId: string,
    fields: Record<string, string | number | boolean>,
  ): Promise<ColabOneWorkflowDraft | null> {
    const wfId = (workflowId || "").trim();
    if (!wfId) {
      return Promise.resolve(null);
    }
    const { shareMode, storageKey } = captureDraftContext();
    const safeFields = sanitizePrimitiveRecord(fields);
    return enqueueWrite(wfId, async () => {
      const now = Date.now() / 1000;
      if (shareMode) {
        if (!storageKey) {
          return null;
        }
        return writeLocalDraft(
          resolveStorage(),
          wfId,
          safeFields,
          now,
          storageKey,
        );
      }
      try {
        const res = await invoker("comfyui.draft.save", {
          workflow_id: wfId,
          fields: safeFields,
        });
        const draft =
          normalizeDraft(res?.draft, wfId) ?? {
            workflow_id: wfId,
            fields: safeFields,
            media: {},
            updated_at: now,
          };
        writeLocalDraft(
          resolveStorage(),
          wfId,
          draft.fields,
          draft.updated_at,
          storageKey,
        );
        return draft;
      } catch (err) {
        if (canFallback(err)) {
          return writeLocalDraft(
            resolveStorage(),
            wfId,
            safeFields,
            now,
            storageKey,
          );
        }
        throw err;
      }
    });
  }

  function saveColabOneWorkflowDraftMedia(
    workflowId: string,
    fieldKey: string,
    payload: ColabOneWorkflowDraftMediaSaveInput,
  ): Promise<ColabOneWorkflowDraftMediaMeta | null> {
    const wfId = (workflowId || "").trim();
    const key = (fieldKey || "").trim();
    if (!wfId || !key) {
      return Promise.resolve(null);
    }
    const { shareMode } = captureDraftContext();
    const snapshot = {
      workflow_id: wfId,
      field_key: key,
      filename: payload.filename,
      kind: payload.kind,
      data: payload.data,
    };
    return enqueueWrite(wfId, async () => {
      if (shareMode) {
        return null;
      }
      try {
        const res = await invoker("comfyui.draft.media.save", snapshot);
        return normalizeMediaMeta(res?.media);
      } catch (err) {
        if (canFallback(err)) {
          return null;
        }
        throw err;
      }
    });
  }

  async function readColabOneWorkflowDraftMedia(
    workflowId: string,
    fieldKey: string,
  ): Promise<ColabOneWorkflowDraftMediaPayload | null> {
    const wfId = (workflowId || "").trim();
    const key = (fieldKey || "").trim();
    if (!wfId || !key) {
      return null;
    }
    const { shareMode } = captureDraftContext();
    await waitForPendingWrites(wfId);
    if (shareMode) {
      return null;
    }
    try {
      const res = await invoker("comfyui.draft.media.read", {
        workflow_id: wfId,
        field_key: key,
      });
      return normalizeMediaPayload(res);
    } catch (err) {
      if (canFallback(err)) {
        return null;
      }
      throw err;
    }
  }

  function deleteColabOneWorkflowDraftMedia(
    workflowId: string,
    fieldKey: string,
  ): Promise<void> {
    const wfId = (workflowId || "").trim();
    const key = (fieldKey || "").trim();
    if (!wfId || !key) {
      return Promise.resolve();
    }
    const { shareMode } = captureDraftContext();
    return enqueueWrite(wfId, async () => {
      if (shareMode) {
        return;
      }
      try {
        await invoker("comfyui.draft.media.delete", {
          workflow_id: wfId,
          field_key: key,
        });
      } catch (err) {
        if (canFallback(err)) {
          return;
        }
        throw err;
      }
    });
  }

  function clearColabOneWorkflowDraft(workflowId: string): Promise<void> {
    const wfId = (workflowId || "").trim();
    if (!wfId) {
      return Promise.resolve();
    }
    const { shareMode, storageKey } = captureDraftContext();
    return enqueueWrite(wfId, async () => {
      if (shareMode) {
        if (storageKey) {
          deleteLocalDraft(resolveStorage(), wfId, storageKey);
        }
        return;
      }
      try {
        await invoker("comfyui.draft.clear", { workflow_id: wfId });
        deleteLocalDraft(resolveStorage(), wfId, storageKey);
      } catch (err) {
        if (canFallback(err)) {
          deleteLocalDraft(resolveStorage(), wfId, storageKey);
          return;
        }
        throw err;
      }
    });
  }

  return {
    loadColabOneWorkflowDraft,
    saveColabOneWorkflowDraft,
    saveColabOneWorkflowDraftMedia,
    readColabOneWorkflowDraftMedia,
    deleteColabOneWorkflowDraftMedia,
    clearColabOneWorkflowDraft,
  };
}

const defaultColabOneWorkflowDraftClient = createColabOneWorkflowDraftClient();

export const loadColabOneWorkflowDraft =
  defaultColabOneWorkflowDraftClient.loadColabOneWorkflowDraft;
export const saveColabOneWorkflowDraft =
  defaultColabOneWorkflowDraftClient.saveColabOneWorkflowDraft;
export const saveColabOneWorkflowDraftMedia =
  defaultColabOneWorkflowDraftClient.saveColabOneWorkflowDraftMedia;
export const readColabOneWorkflowDraftMedia =
  defaultColabOneWorkflowDraftClient.readColabOneWorkflowDraftMedia;
export const deleteColabOneWorkflowDraftMedia =
  defaultColabOneWorkflowDraftClient.deleteColabOneWorkflowDraftMedia;
export const clearColabOneWorkflowDraft =
  defaultColabOneWorkflowDraftClient.clearColabOneWorkflowDraft;

export type ColabOneWorkflowDraftStatus =
  | "idle"
  | "saving"
  | "saved"
  | "restored";

export const COLABONE_DRAFT_STATUS_VISIBLE_MS = 3000;

export interface ColabOneDraftStatusControllerOptions {
  onStatusChange: (status: ColabOneWorkflowDraftStatus) => void;
  isVisible?: () => boolean;
  visibleMs?: number;
}

export interface ColabOneDraftStatusController {
  getStatus(): ColabOneWorkflowDraftStatus;
  setStatus(next: ColabOneWorkflowDraftStatus): void;
  clearIfSaving(): void;
  onVisibilityChange(visible: boolean): void;
  dispose(): void;
}

export function createColabOneDraftStatusController(
  options: ColabOneDraftStatusControllerOptions,
): ColabOneDraftStatusController {
  const visibleMs =
    typeof options.visibleMs === "number" &&
    Number.isFinite(options.visibleMs) &&
    options.visibleMs > 0
      ? options.visibleMs
      : COLABONE_DRAFT_STATUS_VISIBLE_MS;
  const isVisible = options.isVisible ?? (() => true);
  let current: ColabOneWorkflowDraftStatus = "idle";
  let hideTimer: ReturnType<typeof setTimeout> | null = null;
  let disposed = false;

  function cancelHideTimer() {
    if (hideTimer !== null) {
      clearTimeout(hideTimer);
      hideTimer = null;
    }
  }

  function scheduleAutoHide() {
    cancelHideTimer();
    hideTimer = setTimeout(() => {
      hideTimer = null;
      if (disposed) {
        return;
      }
      if (current === "saved" || current === "restored") {
        current = "idle";
        options.onStatusChange("idle");
      }
    }, visibleMs);
  }

  function setStatus(next: ColabOneWorkflowDraftStatus) {
    if (disposed) {
      return;
    }
    cancelHideTimer();
    current = next;
    options.onStatusChange(next);
    if (next === "saved") {
      scheduleAutoHide();
    } else if (next === "restored" && isVisible()) {
      scheduleAutoHide();
    }
  }

  function clearIfSaving() {
    if (current === "saving") {
      setStatus("idle");
    }
  }

  function onVisibilityChange(visible: boolean) {
    if (disposed) {
      return;
    }
    if (visible && current === "restored" && hideTimer === null) {
      scheduleAutoHide();
    }
  }

  function dispose() {
    disposed = true;
    cancelHideTimer();
  }

  return {
    getStatus: () => current,
    setStatus,
    clearIfSaving,
    onVisibilityChange,
    dispose,
  };
}
