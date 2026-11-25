import { colabOneCommand } from "./api";
import { COLABONE_LOCALE_LIST, t, type ColabOneLocale } from "./i18n";
import {
  hasLiveRuntimeGpu,
  resolveColabOneDefaultGpuTarget,
  resolveColabOneGpuTarget,
} from "./modelCompatibility";
import { COLABONE_STORAGE_KEYS } from "./storage";
import type {
  ColabOneComfyTtsEngineSpec,
  ColabOneComfyTtsVoicePreset,
  RuntimeStatus,
  WorkflowField,
  WorkflowOutput,
  WorkflowTemplate,
} from "./types";

export type WorkflowImportResult =
  | { ok: true; workflow: Record<string, unknown> }
  | { ok: false; error: string };

export interface NonCommercialNoticeItem {
  kind: "workflow" | "tts";
  name: string;
  license: string;
  notice: string;
}

export interface NonCommercialNotice {
  restrictedBy: "workflow" | "tts" | "both";
  items: NonCommercialNoticeItem[];
  sourceName: string;
  license: string;
  notice: string;
}

export interface ColabOneWorkflowOverrideOptions {
  uploadedInputIds?: Record<string, string>;
  pendingUploadKeys?: Record<string, unknown>;
}

export interface ColabOneWorkflowOverridePayload {
  overrides: Record<string, string | number | boolean>;
  inputs: Record<string, string>;
}

export interface ColabOneOutputAssetChunk {
  data?: unknown;
  offset?: unknown;
  size?: unknown;
  sha256?: unknown;
  mime?: unknown;
}

export type ColabOneOutputChunkFetcher = (
  id: string,
  offset: number,
) => Promise<ColabOneOutputAssetChunk>;

const CHUNK_SIZE = 512 * 1024;
const MAX_OUTPUT_BYTES = 512 * 1024 ** 2;
const MAX_WORKFLOW_BYTES = 2 * 1024 * 1024;
const MAX_WORKFLOW_NODES = 500;
const MAX_CLASS_TYPE_LENGTH = 160;
const NODE_ID_RE = /^[a-zA-Z0-9_:-]{1,80}$/;
const SHA256_HEX_RE = /^[0-9a-f]{64}$/;
const STRICT_PUBLIC_HOST_RE =
  /^(\[[0-9a-fA-F:]+\]|[A-Za-z0-9.-]+)(?::([0-9]{1,5}))?$/;
const SCHEMELESS_HOST_PORT_RE =
  /^(?:\[[0-9a-fA-F:]+\]|localhost|[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+):[0-9]{1,5}(?:$|[/?#])/i;
const EXPLICIT_SCHEME_RE = /^[a-zA-Z][a-zA-Z0-9+.-]*:/;

const ALLOWED_PREVIEW_MIMES = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
  "video/mp4",
  "video/webm",
  "audio/wav",
  "audio/mpeg",
  "audio/flac",
]);

export const CANVAS_FORMAT_ERROR = t("comfy.errCanvasFormat", undefined, "zh-CN");

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasOwn(
  record: Record<string, unknown> | undefined | null,
  key: string,
): boolean {
  return Boolean(record) && Object.prototype.hasOwnProperty.call(record, key);
}

function utf8ByteLength(text: string): number {
  return new TextEncoder().encode(text).byteLength;
}

function normalizeZero(num: number): number {
  return num === 0 ? 0 : num;
}

function parseBooleanValue(value: unknown, fallback: boolean): boolean {
  if (typeof value === "boolean") {
    return value;
  }
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (normalized === "true" || normalized === "1") {
      return true;
    }
    if (normalized === "false" || normalized === "0") {
      return false;
    }
    return fallback;
  }
  if (typeof value === "number") {
    return Number.isFinite(value) ? value !== 0 : fallback;
  }
  return fallback;
}

function parseFiniteNumber(value: unknown): number | undefined {
  if (typeof value === "number") {
    return Number.isFinite(value) ? value : undefined;
  }
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) {
      return undefined;
    }
    const num = Number(trimmed);
    return Number.isFinite(num) ? num : undefined;
  }
  return undefined;
}

function resolveNumericFieldValue(value: unknown, field: WorkflowField): number {
  const resolved =
    parseFiniteNumber(value) ??
    parseFiniteNumber(field.default) ??
    (typeof field.min_value === "number" && Number.isFinite(field.min_value)
      ? field.min_value
      : 0);
  return field.field_type === "integer"
    ? normalizeZero(Math.round(resolved))
    : normalizeZero(resolved);
}

export function parseWorkflowImportJSON(raw: string): WorkflowImportResult {
  const trimmed = (raw || "").trim();
  if (!trimmed) {
    return { ok: false, error: t("comfy.errEmptyJson") };
  }

  if (utf8ByteLength(trimmed) > MAX_WORKFLOW_BYTES) {
    return { ok: false, error: t("comfy.errImportTooLarge") };
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return { ok: false, error: t("comfy.errInvalidJson") };
  }

  if (!isPlainRecord(parsed)) {
    return { ok: false, error: t("comfy.errTopLevelObject") };
  }

  if (
    Array.isArray(parsed.nodes) &&
    (Array.isArray(parsed.links) || parsed.version !== undefined)
  ) {
    return { ok: false, error: t("comfy.errCanvasFormat") };
  }

  const entries = Object.entries(parsed);
  if (entries.length === 0) {
    return { ok: false, error: t("comfy.errNoNodes") };
  }

  if (entries.length > MAX_WORKFLOW_NODES) {
    return { ok: false, error: t("comfy.errImportTooManyNodes") };
  }

  for (const [nodeId, nodeValue] of entries) {
    if (!NODE_ID_RE.test(nodeId)) {
      return {
        ok: false,
        error: t("comfy.errImportInvalidNodeId", { id: nodeId }),
      };
    }
    if (!isPlainRecord(nodeValue)) {
      return {
        ok: false,
        error: t("comfy.errInvalidNode", { id: nodeId }),
      };
    }
    if (
      typeof nodeValue.class_type !== "string" ||
      !nodeValue.class_type.trim()
    ) {
      return {
        ok: false,
        error: t("comfy.errMissingClassType", { id: nodeId }),
      };
    }
    if (nodeValue.class_type.trim().length > MAX_CLASS_TYPE_LENGTH) {
      return {
        ok: false,
        error: t("comfy.errImportInvalidClassType", { id: nodeId }),
      };
    }
    if (!isPlainRecord(nodeValue.inputs)) {
      return {
        ok: false,
        error: t("comfy.errMissingInputs", { id: nodeId }),
      };
    }
    for (const inputKey of Object.keys(nodeValue.inputs)) {
      if (!inputKey.trim()) {
        return {
          ok: false,
          error: t("comfy.errImportInvalidInputKey", { id: nodeId }),
        };
      }
    }
  }

  return { ok: true, workflow: parsed };
}

export const COLABONE_CANONICAL_VISUAL_PROMPT =
  "Static medium shot of an engaging presenter speaking warmly and naturally to the camera, making expressive, natural hand gestures and open-palm movements to emphasize points while talking, lively facial expressions, subtle eyebrow raises, natural head tilts, shoulder rhythm, natural blinking, clear lip articulation synced with speech, soft studio lighting, sharp focus, stable background";

export const COLABONE_LEGACY_VISUAL_PROMPTS: ReadonlySet<string> = new Set([
  "A clear studio portrait of a speaker talking naturally to the camera with expressive facial movements, gentle head motion, natural blinking, sharp focus, soft studio lighting, stable background",
  "清晰演播室人像镜头，主播面向镜头自然说话，面部表情生动自然，头部轻微摆动，自然眨眼，焦距锐利，柔和演播室灯光，背景稳定不抖动",
  "清晰攝影棚人像鏡頭，主播面向鏡頭自然說話，臉部表情生動自然，頭部輕微擺動，自然眨眼，焦距銳利，柔和攝影棚燈光，背景穩定不晃動",
  "清晰錄影廠人像鏡頭，主播面向鏡頭自然說話，面部表情生動自然，頭部輕微擺動，自然眨眼，焦距銳利，柔和錄影廠燈光，背景穩定不晃動",
  "鮮明なスタジオのポートレート映像。カメラに向かって自然に話す話者、豊かな表情、穏やかな頭の動き、自然なまばたき、シャープなピント、柔らかなスタジオ照明、安定した背景",
  "선명한 스튜디오 인물 샷, 카메라를 향해 자연스럽게 말하는 진행자, 생동감 있는 얼굴 표정, 부드러운 머리 움직임, 자연스러운 눈 깜빡임, 또렷한 초점, 부드러운 스튜디오 조명, 안정적인 배경",
]);

export function isColabOnePresetVisualPrompt(value: unknown): boolean {
  if (typeof value !== "string") {
    return false;
  }
  const trimmed = value.trim();
  if (!trimmed) {
    return false;
  }
  if (
    trimmed === COLABONE_CANONICAL_VISUAL_PROMPT ||
    COLABONE_LEGACY_VISUAL_PROMPTS.has(trimmed)
  ) {
    return true;
  }
  for (const meta of COLABONE_LOCALE_LIST) {
    if (
      trimmed === t("comfy.defaultVisualPrompt", undefined, meta.code).trim()
    ) {
      return true;
    }
  }
  return false;
}

export function resolveColabOneWorkflowFieldDefault(
  field: WorkflowField,
  locale?: ColabOneLocale,
): string | number | boolean {
  const hasDefault = field.default !== undefined && field.default !== null;
  if (field.field_type === "boolean") {
    return hasDefault ? parseBooleanValue(field.default, false) : false;
  }
  if (field.field_type === "integer" || field.field_type === "number") {
    return resolveNumericFieldValue(field.default, field);
  }
  if (field.field_type === "select") {
    const trimmedDefault = hasDefault ? String(field.default).trim() : "";
    if (trimmedDefault !== "") {
      return trimmedDefault;
    }
    if (field.options && field.options.length > 0) {
      return field.options[0];
    }
    return "";
  }
  if (
    typeof field.default === "string" &&
    isColabOnePresetVisualPrompt(field.default)
  ) {
    return t("comfy.defaultVisualPrompt", undefined, locale);
  }
  return hasDefault ? String(field.default) : "";
}

export function defaultFieldValues(
  fields: WorkflowField[],
  locale?: ColabOneLocale,
): Record<string, string | number | boolean> {
  const values: Record<string, string | number | boolean> = {};
  for (const field of fields || []) {
    values[field.key] = resolveColabOneWorkflowFieldDefault(field, locale);
  }
  return values;
}

export function remapColabOnePresetPromptFieldsForLocale(
  fields: WorkflowField[] | undefined,
  values: Record<string, string | number | boolean>,
  nextLocale?: ColabOneLocale,
): Record<string, string | number | boolean> {
  const next: Record<string, string | number | boolean> = { ...values };
  for (const field of fields || []) {
    if (
      typeof field.default === "string" &&
      isColabOnePresetVisualPrompt(field.default) &&
      hasOwn(values, field.key) &&
      isColabOnePresetVisualPrompt(values[field.key])
    ) {
      next[field.key] = t("comfy.defaultVisualPrompt", undefined, nextLocale);
    }
  }
  return next;
}

export function resolveColabOneWorkflowFieldPlaceholder(
  field: WorkflowField,
  locale?: ColabOneLocale,
): string {
  if (field.field_type !== "text" && field.field_type !== "textarea") {
    return "";
  }
  switch (field.key) {
    case "pipeline.script_text":
      return t("comfy.placeholderScriptText", undefined, locale);
    case "pipeline.voice_ref_text":
      return t("comfy.placeholderVoiceRefText", undefined, locale);
    case "pipeline.voice_prompt":
      return t("comfy.placeholderVoicePrompt", undefined, locale);
    case "pipeline.emotion_prompt":
      return t("comfy.placeholderEmotionPrompt", undefined, locale);
    case "5.positive_prompt":
      return t("comfy.placeholderVisualPrompt", undefined, locale);
    default:
      break;
  }
  if (
    typeof field.default === "string" &&
    isColabOnePresetVisualPrompt(field.default)
  ) {
    return t("comfy.placeholderVisualPrompt", undefined, locale);
  }
  const combined = `${field.input_name || ""} ${field.label || ""}`.toLowerCase();
  if (
    combined.includes("negative") ||
    combined.includes("反向") ||
    combined.includes("负向") ||
    combined.includes("負向")
  ) {
    return t("comfy.placeholderNegativePrompt", undefined, locale);
  }
  if (
    combined.includes("prompt") ||
    combined.includes("positive") ||
    combined.includes("正向") ||
    combined.includes("提示词") ||
    combined.includes("提示詞")
  ) {
    return t("comfy.placeholderGenericPrompt", undefined, locale);
  }
  return t("comfy.placeholderGenericText", undefined, locale);
}

export function buildColabOneWorkflowOverrides(
  wf: WorkflowTemplate | undefined,
  values: Record<string, string | number | boolean>,
  visibleFields?: WorkflowField[],
  opts?: ColabOneWorkflowOverrideOptions,
): ColabOneWorkflowOverridePayload {
  const overrides: Record<string, string | number | boolean> = {};
  const inputs: Record<string, string> = {};
  if (!wf) {
    return { overrides, inputs };
  }

  const candidateFields = visibleFields ?? wf.fields ?? [];
  const uploadedInputIds = opts?.uploadedInputIds;
  const pendingUploadKeys = opts?.pendingUploadKeys;

  for (const field of candidateFields) {
    if (!isWorkflowFieldVisible(field, values)) {
      continue;
    }

    const rawValue = hasOwn(values, field.key) ? values[field.key] : undefined;

    if (field.field_type === "image" || field.field_type === "audio") {
      const rawAssetId = hasOwn(uploadedInputIds, field.key)
        ? uploadedInputIds?.[field.key]
        : undefined;
      const assetId = typeof rawAssetId === "string" ? rawAssetId.trim() : "";
      if (assetId) {
        inputs[field.key] = assetId;
        continue;
      }
      if (
        hasOwn(pendingUploadKeys, field.key) &&
        Boolean(pendingUploadKeys?.[field.key])
      ) {
        continue;
      }
      if (typeof rawValue === "string") {
        const trimmedMedia = rawValue.trim();
        if (trimmedMedia) {
          overrides[field.key] = trimmedMedia;
        }
      }
      continue;
    }

    if (field.field_type === "boolean") {
      const fallback = parseBooleanValue(field.default, false);
      overrides[field.key] = parseBooleanValue(rawValue, fallback);
    } else if (
      field.field_type === "integer" ||
      field.field_type === "number"
    ) {
      overrides[field.key] = resolveNumericFieldValue(rawValue, field);
    } else if (field.field_type === "select") {
      const trimmed =
        rawValue !== undefined && rawValue !== null
          ? String(rawValue).trim()
          : "";
      if (trimmed !== "") {
        overrides[field.key] = trimmed;
      } else {
        const defaultStr =
          field.default !== undefined && field.default !== null
            ? String(field.default).trim()
            : "";
        overrides[field.key] = defaultStr || field.options?.[0] || "";
      }
    } else {
      overrides[field.key] =
        rawValue !== undefined && rawValue !== null
          ? String(rawValue).trim()
          : "";
    }
  }

  return { overrides, inputs };
}

export function formatOutputSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  const roundedBytes = Math.round(bytes);
  if (roundedBytes < 1024) {
    return `${roundedBytes} B`;
  }
  const kb = bytes / 1024;
  if (Number(kb.toFixed(1)) < 1024) {
    return `${kb.toFixed(1)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }
}

function decodeBase64(data: string): Uint8Array {
  let binary: string;
  try {
    binary = atob(data);
  } catch {
    throw new Error(t("comfy.errInvalidOutputTransfer"));
  }
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

export async function outputBlobURL(
  idOrOutput: string | Pick<WorkflowOutput, "id">,
  signal?: AbortSignal,
): Promise<string>;
export async function outputBlobURL(
  idOrOutput: string | Pick<WorkflowOutput, "id">,
  fetchChunk?: ColabOneOutputChunkFetcher,
  signal?: AbortSignal,
): Promise<string>;
export async function outputBlobURL(
  idOrOutput: string | Pick<WorkflowOutput, "id">,
  fetchChunkOrSignal?: ColabOneOutputChunkFetcher | AbortSignal,
  maybeSignal?: AbortSignal,
): Promise<string> {
  const id =
    typeof idOrOutput === "string"
      ? idOrOutput
      : typeof idOrOutput?.id === "string"
        ? idOrOutput.id
        : "";
  if (!id || !id.trim()) {
    throw new Error(t("comfy.errInvalidOutputId"));
  }

  const fetchChunk: ColabOneOutputChunkFetcher =
    typeof fetchChunkOrSignal === "function"
      ? fetchChunkOrSignal
      : (assetId, chunkOffset) =>
          colabOneCommand("comfyui.output.asset", {
            id: assetId,
            offset: chunkOffset,
          });
  const signal: AbortSignal | undefined =
    typeof fetchChunkOrSignal === "function"
      ? maybeSignal
      : fetchChunkOrSignal ?? maybeSignal;

  const chunks: Uint8Array[] = [];
  let offset = 0;
  let totalSize = -1;
  let expectedSha256 = "";
  let mime = "";

  while (totalSize < 0 || offset < totalSize) {
    throwIfAborted(signal);
    const part = await fetchChunk(id, offset);
    throwIfAborted(signal);

    if (!part || typeof part.data !== "string") {
      throw new Error(t("comfy.errInvalidOutputTransfer"));
    }
    const decoded = decodeBase64(part.data);

    if (totalSize < 0) {
      const size = typeof part.size === "number" ? part.size : Number.NaN;
      if (!Number.isSafeInteger(size) || size <= 0 || size > MAX_OUTPUT_BYTES) {
        throw new Error(t("comfy.errInvalidOutputSize"));
      }
      totalSize = size;
      const rawMime = typeof part.mime === "string" ? part.mime : "";
      const candidate = rawMime.split(";")[0].trim().toLowerCase();
      if (!ALLOWED_PREVIEW_MIMES.has(candidate)) {
        throw new Error(t("comfy.errUnsupportedPreview"));
      }
      mime = candidate;
    }

    let partSha256 = "";
    if (part.sha256 !== undefined) {
      if (typeof part.sha256 !== "string") {
        throw new Error(t("comfy.errOutputChecksum"));
      }
      const normalizedSha = part.sha256.trim().toLowerCase();
      if (!SHA256_HEX_RE.test(normalizedSha)) {
        throw new Error(t("comfy.errOutputChecksum"));
      }
      partSha256 = normalizedSha;
      if (!expectedSha256) {
        expectedSha256 = partSha256;
      }
    }

    const invalidOffset =
      offset > 0
        ? part.offset !== offset
        : part.offset !== undefined && part.offset !== 0;
    const invalidSize = part.size !== undefined && part.size !== totalSize;
    const invalidSha256 = Boolean(
      expectedSha256 && partSha256 && partSha256 !== expectedSha256,
    );

    if (
      invalidOffset ||
      invalidSize ||
      invalidSha256 ||
      !decoded.length ||
      decoded.length > CHUNK_SIZE ||
      offset + decoded.length > totalSize
    ) {
      throw new Error(t("comfy.errIncompleteTransfer"));
    }

    chunks.push(decoded);
    offset += decoded.length;
  }

  const merged = new Uint8Array(new ArrayBuffer(totalSize));
  let writeOffset = 0;
  for (let i = 0; i < chunks.length; i += 1) {
    const chunk = chunks[i];
    merged.set(chunk, writeOffset);
    writeOffset += chunk.byteLength;
  }
  chunks.length = 0;

  throwIfAborted(signal);
  if (expectedSha256) {
    if (!globalThis.crypto?.subtle) {
      throw new Error(t("comfy.errOutputChecksum"));
    }
    const digest = await globalThis.crypto.subtle.digest("SHA-256", merged);
    throwIfAborted(signal);
    const actual = Array.from(new Uint8Array(digest), (n) =>
      n.toString(16).padStart(2, "0"),
    ).join("");
    if (actual !== expectedSha256) {
      throw new Error(t("comfy.errOutputChecksum"));
    }
  }
  const blob = new Blob([merged], { type: mime });
  return URL.createObjectURL(blob);
}

function isValidPublicHost(host: string): boolean {
  const match = STRICT_PUBLIC_HOST_RE.exec(host);
  if (!match) return false;
  if (match[2] !== undefined) {
    const port = Number(match[2]);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      return false;
    }
  }
  return true;
}

export function extractPublicHost(url?: string): string {
  if (!url) return "";
  const trimmed = url.trim();
  if (!trimmed) return "";

  let candidate = "";
  if (/^https?:\/\//i.test(trimmed)) {
    try {
      const parsed = new URL(trimmed);
      if (parsed.protocol === "http:" || parsed.protocol === "https:") {
        candidate = parsed.host;
      }
    } catch {
      return "";
    }
  } else if (trimmed.startsWith("//")) {
    try {
      const parsed = new URL(`https:${trimmed}`);
      if (parsed.protocol === "https:") {
        candidate = parsed.host;
      }
    } catch {
      return "";
    }
  } else if (
    !EXPLICIT_SCHEME_RE.test(trimmed) ||
    SCHEMELESS_HOST_PORT_RE.test(trimmed)
  ) {
    try {
      const parsed = new URL(`https://${trimmed.replace(/^\/+/, "")}`);
      if (parsed.protocol === "https:") {
        candidate = parsed.host;
      }
    } catch {
      return "";
    }
  } else {
    return "";
  }

  return isValidPublicHost(candidate) ? candidate : "";
}

export function isWorkflowFieldVisible(
  field: WorkflowField,
  values: Record<string, string | number | boolean>,
): boolean {
  const conditions = field.visible_when;
  if (!conditions) {
    return true;
  }
  const entries = Object.entries(conditions);
  if (entries.length === 0) {
    return true;
  }
  if (!values) {
    return false;
  }
  for (const [key, expected] of entries) {
    if (!Object.prototype.hasOwnProperty.call(values, key)) {
      return false;
    }
    const actual = values[key];
    if (typeof expected === "string") {
      if (typeof actual !== "string" || actual.trim() !== expected.trim()) {
        return false;
      }
    } else if (actual !== expected) {
      return false;
    }
  }
  return true;
}

const CANONICAL_WORKFLOW_FIELD_GROUPS = [
  "basic",
  "media",
  "voice",
  "video",
  "advanced",
] as const;

export function groupWorkflowFields(
  fields: WorkflowField[],
): { group: string; fields: WorkflowField[] }[] {
  const list = fields || [];
  if (list.length === 0) {
    return [];
  }

  const buckets = new Map<string, WorkflowField[]>();
  const otherOrder: string[] = [];
  const canonicalSet = new Set<string>(CANONICAL_WORKFLOW_FIELD_GROUPS);
  let hasAnyGroup = false;

  for (const field of list) {
    const group = (field.group || "").trim().toLowerCase();
    if (group) {
      hasAnyGroup = true;
    }
    let bucket = buckets.get(group);
    if (!bucket) {
      bucket = [];
      buckets.set(group, bucket);
      if (group && !canonicalSet.has(group)) {
        otherOrder.push(group);
      }
    }
    bucket.push(field);
  }

  if (!hasAnyGroup) {
    return [{ group: "", fields: [...list] }];
  }

  const grouped: { group: string; fields: WorkflowField[] }[] = [];
  for (const canonical of CANONICAL_WORKFLOW_FIELD_GROUPS) {
    const bucket = buckets.get(canonical);
    if (bucket && bucket.length > 0) {
      grouped.push({ group: canonical, fields: bucket });
    }
  }
  for (const other of otherOrder) {
    const bucket = buckets.get(other);
    if (bucket && bucket.length > 0) {
      grouped.push({ group: other, fields: bucket });
    }
  }
  const ungrouped = buckets.get("");
  if (ungrouped && ungrouped.length > 0) {
    grouped.push({ group: "", fields: ungrouped });
  }
  return grouped;
}

export function partitionWorkflowFields(fields: WorkflowField[]): {
  primary: WorkflowField[];
  advanced: WorkflowField[];
} {
  const list = fields || [];
  const primary: WorkflowField[] = [];
  const advanced: WorkflowField[] = [];

  for (const field of list) {
    if ((field.group || "").trim().toLowerCase() === "advanced") {
      advanced.push(field);
    } else {
      primary.push(field);
    }
  }

  if (primary.length === 0 && advanced.length > 0) {
    return { primary: [...list], advanced: [] };
  }

  return { primary, advanced };
}

export function resolveSelectedTtsEngine(
  wf: WorkflowTemplate | undefined,
  values: Record<string, string | number | boolean>,
  engines: ColabOneComfyTtsEngineSpec[] | undefined,
): ColabOneComfyTtsEngineSpec | undefined {
  if (!wf || wf.pipeline?.kind !== "digital_human") {
    return undefined;
  }
  if (!engines || engines.length === 0) {
    return undefined;
  }
  if (String(values?.["pipeline.audio_source"] ?? "tts").trim() !== "tts") {
    return undefined;
  }
  const rawSelected = hasOwn(values, "pipeline.tts_engine")
    ? values["pipeline.tts_engine"]
    : undefined;
  const engineId =
    typeof rawSelected === "string" && rawSelected.trim()
      ? rawSelected.trim()
      : (wf.pipeline.default_tts_engine || "").trim();
  if (!engineId) {
    return undefined;
  }
  const supported = wf.pipeline.supported_tts_engines;
  if (supported && supported.length > 0 && !supported.includes(engineId)) {
    return undefined;
  }
  return engines.find((engine) => engine.id === engineId);
}

export function workflowModelsTotalBytes(
  wf: WorkflowTemplate | undefined,
): number {
  if (!wf?.models?.length) {
    return 0;
  }
  let total = 0;
  for (const model of wf.models) {
    if (
      typeof model.size === "number" &&
      Number.isFinite(model.size) &&
      model.size > 0
    ) {
      total += model.size;
    }
  }
  return total;
}

const ALLOWED_AUDIO_PREVIEW_MIMES = new Set([
  "audio/wav",
  "audio/mpeg",
  "audio/flac",
]);

const NON_COMMERCIAL_TTS_ENGINE_IDS = new Set([
  "fish_drama3",
  "breeze_tts2",
  "indextts2",
  "fish_s2_pro",
]);

const FALLBACK_MALE_VOICE_PRESET_IDS = new Set([
  "chen_jingzhou",
  "lin_chenxu",
  "zhou_moyan",
  "gu_zhengyuan",
  "su_hechuan",
]);

export const COLABONE_DEFAULT_VOICE_PREVIEW_TEXT =
  "大家好，欢迎来到今天的节目。我们将用清晰自然的声音，为您娓娓道来每一个精彩细节，希望这段分享能带给您轻松愉快的收听体验。";

export type ColabOneVoicePresetGroupKey =
  | "female"
  | "male"
  | "neutral"
  | "official"
  | "custom";

export interface ColabOneVoicePresetOption {
  id: string;
  label: string;
  preset?: ColabOneComfyTtsVoicePreset;
}

export interface ColabOneVoicePresetGroup {
  key: ColabOneVoicePresetGroupKey;
  options: ColabOneVoicePresetOption[];
}

export function resolveColabOneVoicePresetGroups(
  field: WorkflowField,
  engine?: ColabOneComfyTtsEngineSpec,
): ColabOneVoicePresetGroup[] {
  const buckets: Record<
    ColabOneVoicePresetGroupKey,
    ColabOneVoicePresetOption[]
  > = {
    female: [],
    male: [],
    neutral: [],
    official: [],
    custom: [],
  };
  const seen = new Set<string>();
  const optionLabels = field?.option_labels || {};
  const fieldOptions = Array.isArray(field?.options) ? field.options : [];
  const enginePresets = Array.isArray(engine?.voice_presets)
    ? engine.voice_presets
    : [];

  const resolveLabel = (
    id: string,
    preset?: ColabOneComfyTtsVoicePreset,
  ): string => {
    const mapped =
      typeof optionLabels[id] === "string" ? optionLabels[id].trim() : "";
    if (mapped) return mapped;
    const presetName =
      typeof preset?.name === "string" ? preset.name.trim() : "";
    if (presetName) return presetName;
    return id;
  };

  if (enginePresets.length > 0) {
    for (const preset of enginePresets) {
      const id = typeof preset?.id === "string" ? preset.id.trim() : "";
      if (!id || seen.has(id)) continue;
      seen.add(id);
      const item: ColabOneVoicePresetOption = {
        id,
        label: resolveLabel(id, preset),
        preset,
      };
      if (id === "custom") {
        buckets.custom.push(item);
      } else if (
        typeof preset.speaker_id === "string" &&
        preset.speaker_id.trim().length > 0
      ) {
        buckets.official.push(item);
      } else if (preset.gender === "male") {
        buckets.male.push(item);
      } else if (preset.gender === "female") {
        buckets.female.push(item);
      } else if (preset.gender === "neutral") {
        buckets.neutral.push(item);
      } else if (FALLBACK_MALE_VOICE_PRESET_IDS.has(id)) {
        buckets.male.push(item);
      } else {
        buckets.neutral.push(item);
      }
    }
    for (const rawOpt of fieldOptions) {
      const id = typeof rawOpt === "string" ? rawOpt.trim() : "";
      if (id === "custom" && !seen.has(id)) {
        seen.add(id);
        buckets.custom.push({
          id,
          label: resolveLabel(id),
        });
      }
    }
  } else {
    for (const rawOpt of fieldOptions) {
      const id = typeof rawOpt === "string" ? rawOpt.trim() : "";
      if (!id || seen.has(id)) continue;
      seen.add(id);
      const label = resolveLabel(id);
      if (id === "custom") {
        buckets.custom.push({ id, label });
      } else if (id.startsWith("qwen_")) {
        buckets.official.push({ id, label });
      } else if (FALLBACK_MALE_VOICE_PRESET_IDS.has(id)) {
        buckets.male.push({ id, label });
      } else {
        buckets.female.push({ id, label });
      }
    }
  }

  const order: ColabOneVoicePresetGroupKey[] = [
    "female",
    "male",
    "neutral",
    "official",
    "custom",
  ];
  const result: ColabOneVoicePresetGroup[] = [];
  for (const key of order) {
    if (buckets[key].length > 0) {
      result.push({ key, options: buckets[key] });
    }
  }
  return result;
}

export function resolveColabOneSelectedVoicePreset(
  value: string,
  engine?: ColabOneComfyTtsEngineSpec,
): ColabOneComfyTtsVoicePreset | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  const normalized = value.trim();
  if (!normalized || normalized === "custom") {
    return undefined;
  }
  const presets = engine?.voice_presets;
  if (!Array.isArray(presets) || presets.length === 0) {
    return undefined;
  }
  return presets.find(
    (preset) =>
      typeof preset?.id === "string" && preset.id.trim() === normalized,
  );
}

export function isColabOneVoicePresetSupportedByEngine(
  presetId: string,
  field: WorkflowField | undefined,
  engine?: ColabOneComfyTtsEngineSpec,
): boolean {
  const normalized = typeof presetId === "string" ? presetId.trim() : "";
  if (!normalized) {
    return false;
  }
  if (normalized === "custom") {
    return true;
  }
  if (Array.isArray(engine?.voice_presets) && engine.voice_presets.length > 0) {
    return engine.voice_presets.some(
      (preset) =>
        typeof preset?.id === "string" && preset.id.trim() === normalized,
    );
  }
  if (Array.isArray(field?.options) && field.options.length > 0) {
    return field.options.includes(normalized);
  }
  return false;
}

export function resolveColabOneFallbackVoicePreset(
  field: WorkflowField | undefined,
  engine?: ColabOneComfyTtsEngineSpec,
): string {
  if (isColabOneVoicePresetSupportedByEngine("shen_zhiyi", field, engine)) {
    return "shen_zhiyi";
  }
  const fieldDefault =
    typeof field?.default === "string" ? field.default.trim() : "";
  if (
    fieldDefault &&
    isColabOneVoicePresetSupportedByEngine(fieldDefault, field, engine)
  ) {
    return fieldDefault;
  }
  const firstPreset = engine?.voice_presets?.find(
    (preset) => typeof preset?.id === "string" && preset.id.trim().length > 0,
  );
  if (firstPreset) {
    return firstPreset.id.trim();
  }
  return "shen_zhiyi";
}

export function resolveColabOneVoicePreviewDefaultText(
  preset?: ColabOneComfyTtsVoicePreset,
): string {
  const custom =
    typeof preset?.preview_text === "string" ? preset.preview_text.trim() : "";
  return custom || COLABONE_DEFAULT_VOICE_PREVIEW_TEXT;
}

function decodeColabOneBase64AudioPayload(
  base64Data: string,
  mime = "audio/wav",
): { buffer: Uint8Array<ArrayBuffer>; mime: string } {
  const normalizedMime = String(mime || "audio/wav")
    .split(";")[0]
    .trim()
    .toLowerCase();
  if (!ALLOWED_AUDIO_PREVIEW_MIMES.has(normalizedMime)) {
    throw new Error(t("comfy.errUnsupportedPreview"));
  }
  const raw = typeof base64Data === "string" ? base64Data.trim() : "";
  if (!raw) {
    throw new Error(t("comfy.errInvalidOutputTransfer"));
  }
  const cleaned = raw.replace(/\s+/g, "");
  const decoded = decodeBase64(cleaned);
  if (!decoded.byteLength) {
    throw new Error(t("comfy.errInvalidOutputTransfer"));
  }
  const buffer = new Uint8Array(new ArrayBuffer(decoded.byteLength));
  buffer.set(decoded);
  return { buffer, mime: normalizedMime };
}

export function decodeColabOneBase64AudioUrl(
  base64Data: string,
  mime = "audio/wav",
): string {
  const { buffer, mime: normalizedMime } = decodeColabOneBase64AudioPayload(
    base64Data,
    mime,
  );
  const blob = new Blob([buffer], { type: normalizedMime });
  return URL.createObjectURL(blob);
}

export async function verifyAndDecodeColabOneBase64AudioUrl(
  base64Data: string,
  mime = "audio/wav",
  expectedSha256?: string,
): Promise<string> {
  const { buffer, mime: normalizedMime } = decodeColabOneBase64AudioPayload(
    base64Data,
    mime,
  );
  if (expectedSha256 !== undefined && expectedSha256 !== null) {
    if (typeof expectedSha256 !== "string") {
      throw new Error(t("comfy.errInvalidOutputTransfer"));
    }
  }
  const expected =
    typeof expectedSha256 === "string"
      ? expectedSha256.trim().toLowerCase()
      : "";
  if (expected) {
    if (!SHA256_HEX_RE.test(expected)) {
      throw new Error(t("comfy.errInvalidOutputTransfer"));
    }
    if (!globalThis.crypto?.subtle?.digest) {
      throw new Error(t("comfy.errOutputChecksum"));
    }
    const digest = await globalThis.crypto.subtle.digest("SHA-256", buffer);
    const actual = Array.from(new Uint8Array(digest), (n) =>
      n.toString(16).padStart(2, "0"),
    ).join("");
    if (actual !== expected) {
      throw new Error(t("comfy.errInvalidOutputTransfer"));
    }
  }
  const blob = new Blob([buffer], { type: normalizedMime });
  return URL.createObjectURL(blob);
}

function formatColabOneMiBAsGiB(mib: number): string {
  if (!Number.isFinite(mib) || mib <= 0) {
    return "0 GB";
  }
  const gib = mib / 1024;
  const formatted = Number.isInteger(gib)
    ? String(gib)
    : gib.toFixed(1).replace(/\.0$/, "");
  return `${formatted} GB`;
}

export function formatColabOneVramGiB(mib: number): string {
  return formatColabOneMiBAsGiB(mib);
}

export function formatColabOneDiskGiB(mib: number): string {
  return formatColabOneMiBAsGiB(mib);
}

export function isColabOneTtsEngineNonCommercial(
  engine?: ColabOneComfyTtsEngineSpec,
): boolean {
  if (!engine) {
    return false;
  }
  return (
    engine.commercial_ok === false ||
    (engine.commercial_ok === undefined &&
      NON_COMMERCIAL_TTS_ENGINE_IDS.has((engine.id || "").trim()))
  );
}

export function hasNonCommercialNotice(
  wf: WorkflowTemplate | undefined,
  selectedTts: ColabOneComfyTtsEngineSpec | undefined,
): NonCommercialNotice | null {
  const wfRestricted = wf?.commercial_ok === false;
  const ttsRestricted = isColabOneTtsEngineNonCommercial(selectedTts);
  if (!wfRestricted && !ttsRestricted) {
    return null;
  }
  const items: NonCommercialNoticeItem[] = [];
  if (wfRestricted && wf) {
    items.push({
      kind: "workflow",
      name: (wf.name || "").trim() || wf.id || "",
      license: (wf.license || "").trim(),
      notice: (wf.license_notice || "").trim(),
    });
  }
  if (ttsRestricted && selectedTts) {
    items.push({
      kind: "tts",
      name: (selectedTts.name || "").trim() || selectedTts.id || "",
      license: (selectedTts.license || "").trim(),
      notice: (selectedTts.license_notice || "").trim(),
    });
  }
  if (items.length === 0) {
    return null;
  }
  const restrictedBy: NonCommercialNotice["restrictedBy"] =
    items.length === 2 ? "both" : items[0].kind;
  const names = items.map((item) => item.name).filter(Boolean);
  const licenses = Array.from(
    new Set(items.map((item) => item.license).filter(Boolean)),
  );
  const notices = Array.from(
    new Set(items.map((item) => item.notice).filter(Boolean)),
  );
  return {
    restrictedBy,
    items,
    sourceName: names.join(" + "),
    license: licenses.join(" / "),
    notice: notices.join(" "),
  };
}

export const resolveNonCommercialNotice = hasNonCommercialNotice;

export const CLONE_ONLY_TTS_ENGINE_IDS: ReadonlySet<string> = new Set<string>([
  "fish_drama3",
  "moss_tts_v1_5",
  "fish_s2_pro",
  "indextts2",
]);

export function isColabOneVoiceRefRequired(
  wf: WorkflowTemplate | undefined,
  values: Record<string, string | number | boolean> = {},
): boolean {
  if (!wf || wf.pipeline?.kind !== "digital_human") {
    return false;
  }
  const rawAudioSource = hasOwn(values, "pipeline.audio_source")
    ? values["pipeline.audio_source"]
    : undefined;
  if (String(rawAudioSource ?? "tts").trim() !== "tts") {
    return false;
  }
  const rawTtsEngine = hasOwn(values, "pipeline.tts_engine")
    ? values["pipeline.tts_engine"]
    : undefined;
  const resolvedTtsEngineId =
    typeof rawTtsEngine === "string" && rawTtsEngine.trim()
      ? rawTtsEngine.trim()
      : (wf.pipeline.default_tts_engine || "").trim();
  const voicePresetField = (wf.fields || []).find(
    (field) => field.key === "pipeline.voice_preset",
  );
  if (!voicePresetField) {
    return resolvedTtsEngineId === "indextts2";
  }
  if (!CLONE_ONLY_TTS_ENGINE_IDS.has(resolvedTtsEngineId)) {
    return false;
  }
  const rawVoicePreset = hasOwn(values, "pipeline.voice_preset")
    ? values["pipeline.voice_preset"]
    : undefined;
  const resolvedVoicePreset =
    rawVoicePreset !== undefined &&
    rawVoicePreset !== null &&
    String(rawVoicePreset).trim() !== ""
      ? String(rawVoicePreset).trim()
      : voicePresetField.default !== undefined &&
          voicePresetField.default !== null
        ? String(voicePresetField.default).trim()
        : "";
  return resolvedVoicePreset === "custom";
}

export function isColabOneVoiceRefTextRequired(
  wf: WorkflowTemplate | undefined,
  values: Record<string, string | number | boolean> = {},
  uploadedInputIds: Record<string, string> = {},
  pendingUploads: Record<string, unknown> = {},
): boolean {
  if (!wf || wf.pipeline?.kind !== "digital_human") {
    return false;
  }
  const rawAudioSource = hasOwn(values, "pipeline.audio_source")
    ? values["pipeline.audio_source"]
    : undefined;
  if (String(rawAudioSource ?? "tts").trim() !== "tts") {
    return false;
  }
  const uploadedId = hasOwn(uploadedInputIds, "pipeline.voice_ref_audio")
    ? uploadedInputIds["pipeline.voice_ref_audio"]
    : undefined;
  const hasUploaded =
    typeof uploadedId === "string" && uploadedId.trim().length > 0;
  const hasPending =
    hasOwn(pendingUploads, "pipeline.voice_ref_audio") &&
    Boolean(pendingUploads["pipeline.voice_ref_audio"]);
  if (!hasUploaded && !hasPending) {
    return false;
  }
  const rawTtsEngine = hasOwn(values, "pipeline.tts_engine")
    ? values["pipeline.tts_engine"]
    : undefined;
  const resolvedTtsEngineId =
    typeof rawTtsEngine === "string" && rawTtsEngine.trim()
      ? rawTtsEngine.trim()
      : (wf.pipeline.default_tts_engine || "").trim();
  if (
    resolvedTtsEngineId === "breeze_tts2" ||
    resolvedTtsEngineId === "fish_drama3"
  ) {
    return true;
  }
  if (resolvedTtsEngineId === "ming_3_tts") {
    const rawEmotionPrompt = hasOwn(values, "pipeline.emotion_prompt")
      ? values["pipeline.emotion_prompt"]
      : undefined;
    return String(rawEmotionPrompt ?? "").trim().length === 0;
  }
  return false;
}

export function missingRequiredWorkflowFields(
  wf: WorkflowTemplate | undefined,
  values: Record<string, string | number | boolean>,
  uploadedInputIds: Record<string, string>,
  pendingUploads: Record<string, unknown>,
): WorkflowField[] {
  if (!wf?.fields?.length) {
    return [];
  }

  const requiresVoiceRefAudio = isColabOneVoiceRefRequired(wf, values);
  const requiresVoiceRefText = isColabOneVoiceRefTextRequired(
    wf,
    values,
    uploadedInputIds,
    pendingUploads,
  );

  const missing: WorkflowField[] = [];
  for (const field of wf.fields) {
    const isRequired =
      Boolean(field.required) ||
      (requiresVoiceRefAudio && field.key === "pipeline.voice_ref_audio") ||
      (requiresVoiceRefText && field.key === "pipeline.voice_ref_text");
    if (!isRequired) {
      continue;
    }
    if (!isWorkflowFieldVisible(field, values)) {
      continue;
    }
    if (field.field_type === "image" || field.field_type === "audio") {
      const uploadedId = hasOwn(uploadedInputIds, field.key)
        ? uploadedInputIds[field.key]
        : undefined;
      const hasUploaded =
        typeof uploadedId === "string" && uploadedId.trim().length > 0;
      const hasPending =
        hasOwn(pendingUploads, field.key) && Boolean(pendingUploads[field.key]);
      if (!hasUploaded && !hasPending) {
        missing.push(field);
      }
      continue;
    }
    const value = hasOwn(values, field.key) ? values[field.key] : undefined;
    if (field.field_type === "number" || field.field_type === "integer") {
      if (
        value === undefined ||
        value === null ||
        typeof value === "boolean" ||
        (typeof value === "string" && value.trim() === "") ||
        !Number.isFinite(Number(value))
      ) {
        missing.push(field);
      }
      continue;
    }
    if (field.field_type === "boolean") {
      if (typeof value !== "boolean") {
        missing.push(field);
      }
      continue;
    }
    if (
      value === undefined ||
      value === null ||
      String(value).trim().length === 0
    ) {
      missing.push(field);
    }
  }
  return missing;
}

export const COLABONE_DIGITAL_HUMAN_GROUP_KEY = "colabone-digital-human-group";

export const COLABONE_DIGITAL_HUMAN_ENGINE_SPECIFIC_KEYS: ReadonlySet<string> =
  new Set<string>([
    "pipeline.max_segment_seconds",
    "pipeline.render_quality",
    "pipeline.acceleration_profile",
    "14.steps",
    "15.steps",
  ]);

export interface ColabOneStudioWorkflowEntry {
  key: string;
  isGroup: boolean;
  workflows: WorkflowTemplate[];
  primary: WorkflowTemplate;
}

export interface ColabOneDigitalHumanGpuContext {
  vramMib: number;
  shortLabel: string;
  isLive: boolean;
}

export interface ColabOneDigitalHumanSelection {
  workflowId: string;
  recommendedWorkflowId: string;
  fromUserPreference: boolean;
}

export function isColabOneBuiltinDigitalHumanWorkflow(
  wf?: WorkflowTemplate | null,
): boolean {
  return Boolean(
    wf && wf.source === "builtin" && wf.pipeline?.kind === "digital_human",
  );
}

const COLABONE_LIVE_RUNTIME_GPU_STATES: ReadonlySet<string> = new Set([
  "ready",
  "running",
  "starting",
  "connecting",
]);

export function buildColabOneStudioWorkflowEntries(
  workflows: readonly WorkflowTemplate[] | undefined | null,
): ColabOneStudioWorkflowEntry[] {
  const list = Array.isArray(workflows) ? workflows : [];
  if (list.length === 0) {
    return [];
  }

  const builtinDigitalHumans: WorkflowTemplate[] = [];
  const seenBuiltinIds = new Set<string>();
  for (const wf of list) {
    if (isColabOneBuiltinDigitalHumanWorkflow(wf) && !seenBuiltinIds.has(wf.id)) {
      seenBuiltinIds.add(wf.id);
      builtinDigitalHumans.push(wf);
    }
  }
  if (builtinDigitalHumans.length < 2) {
    return list.map((wf) => ({
      key: wf.id,
      isGroup: false,
      workflows: [wf],
      primary: wf,
    }));
  }

  const entries: ColabOneStudioWorkflowEntry[] = [];
  let groupInserted = false;
  for (const wf of list) {
    if (isColabOneBuiltinDigitalHumanWorkflow(wf)) {
      if (!groupInserted) {
        entries.push({
          key: COLABONE_DIGITAL_HUMAN_GROUP_KEY,
          isGroup: true,
          workflows: builtinDigitalHumans,
          primary: builtinDigitalHumans[0],
        });
        groupInserted = true;
      }
      continue;
    }
    const entryKey =
      wf.id === COLABONE_DIGITAL_HUMAN_GROUP_KEY ? `custom:${wf.id}` : wf.id;
    entries.push({
      key: entryKey,
      isGroup: false,
      workflows: [wf],
      primary: wf,
    });
  }
  return entries;
}

export function resolveColabOneDigitalHumanGpuContext(
  runtime?: RuntimeStatus | null,
  settingsGpu?: string | null,
): ColabOneDigitalHumanGpuContext {
  const normalizedSettingsGpu =
    typeof settingsGpu === "string" ? settingsGpu.trim() : settingsGpu;
  const runtimeState =
    typeof runtime?.state === "string" ? runtime.state.trim().toLowerCase() : "";
  if (runtime && COLABONE_LIVE_RUNTIME_GPU_STATES.has(runtimeState)) {
    const primaryGpu = runtime.gpu;
    if (
      primaryGpu &&
      typeof primaryGpu.total_mib === "number" &&
      Number.isFinite(primaryGpu.total_mib) &&
      primaryGpu.total_mib > 0 &&
      hasLiveRuntimeGpu(runtime)
    ) {
      const target = resolveColabOneGpuTarget(
        "CURRENT",
        runtime,
        normalizedSettingsGpu,
      );
      return {
        vramMib: target.vramMib,
        shortLabel: target.shortLabel,
        isLive: true,
      };
    }
    const resourceGpu = runtime.resources?.gpu;
    if (
      resourceGpu &&
      typeof resourceGpu.total_mib === "number" &&
      Number.isFinite(resourceGpu.total_mib) &&
      resourceGpu.total_mib > 0
    ) {
      const syntheticRuntime: RuntimeStatus = {
        ...runtime,
        gpu: resourceGpu,
      };
      const target = resolveColabOneGpuTarget(
        "CURRENT",
        syntheticRuntime,
        normalizedSettingsGpu,
      );
      return {
        vramMib: target.vramMib,
        shortLabel: target.shortLabel,
        isLive: true,
      };
    }
  }
  const targetId = resolveColabOneDefaultGpuTarget(null, normalizedSettingsGpu);
  const target = resolveColabOneGpuTarget(targetId, null, normalizedSettingsGpu);
  return {
    vramMib: target.vramMib,
    shortLabel: target.shortLabel,
    isLive: false,
  };
}

export function resolveColabOneRecommendedDigitalHumanWorkflowId(
  digitalHumanWorkflows: readonly WorkflowTemplate[] | undefined | null,
  runtime?: RuntimeStatus | null,
  settingsGpu?: string | null,
): string {
  const list = (
    Array.isArray(digitalHumanWorkflows) ? digitalHumanWorkflows : []
  ).filter((wf) => isColabOneBuiltinDigitalHumanWorkflow(wf));
  if (list.length === 0) {
    return "";
  }
  if (list.length === 1) {
    return list[0].id;
  }

  const flagship = list.find(
    (wf) =>
      wf.pipeline?.video_backend === "longcat_avatar" ||
      wf.id === "digital-human-longcat",
  );
  const longVideo = list.find(
    (wf) =>
      wf.pipeline?.video_backend === "infinitetalk" ||
      wf.id === "digital-human-infinitetalk",
  );
  const { vramMib } = resolveColabOneDigitalHumanGpuContext(
    runtime,
    settingsGpu,
  );
  const flagshipMinVramMib =
    flagship &&
    typeof flagship.min_vram_mib === "number" &&
    Number.isFinite(flagship.min_vram_mib) &&
    flagship.min_vram_mib > 0
      ? flagship.min_vram_mib
      : 49152;
  if (flagship && vramMib >= flagshipMinVramMib) {
    return flagship.id;
  }
  if (longVideo) {
    return longVideo.id;
  }
  if (flagship) {
    const alternate = list.find((wf) => wf.id !== flagship.id);
    if (alternate) {
      return alternate.id;
    }
  }
  return list[0].id;
}

function resolveColabOneLocalStorage(storage?: Storage | null): Storage | null {
  if (storage !== undefined) {
    return storage;
  }
  try {
    return typeof window !== "undefined" ? (window.localStorage ?? null) : null;
  } catch {
    return null;
  }
}

export function readColabOnePreferredDigitalHumanEngine(
  storage?: Storage | null,
): string {
  const target = resolveColabOneLocalStorage(storage);
  if (!target) {
    return "";
  }
  try {
    const raw = target.getItem(COLABONE_STORAGE_KEYS.comfyDigitalHumanEngine);
    return typeof raw === "string" ? raw.trim() : "";
  } catch {
    return "";
  }
}

export function writeColabOnePreferredDigitalHumanEngine(
  workflowId: string,
  storage?: Storage | null,
): void {
  const target = resolveColabOneLocalStorage(storage);
  if (!target) {
    return;
  }
  const trimmed = typeof workflowId === "string" ? workflowId.trim() : "";
  try {
    if (!trimmed) {
      target.removeItem(COLABONE_STORAGE_KEYS.comfyDigitalHumanEngine);
      return;
    }
    target.setItem(COLABONE_STORAGE_KEYS.comfyDigitalHumanEngine, trimmed);
  } catch {
    // Ignore storage quota or restricted storage errors.
  }
}

export function resolveColabOneInitialDigitalHumanWorkflowId(
  digitalHumanWorkflows: readonly WorkflowTemplate[] | undefined | null,
  runtime?: RuntimeStatus | null,
  settingsGpu?: string | null,
  preferredWorkflowId?: string | null,
): ColabOneDigitalHumanSelection {
  const list = (
    Array.isArray(digitalHumanWorkflows) ? digitalHumanWorkflows : []
  ).filter((wf) => isColabOneBuiltinDigitalHumanWorkflow(wf));
  const recommendedWorkflowId = resolveColabOneRecommendedDigitalHumanWorkflowId(
    list,
    runtime,
    settingsGpu,
  );
  const trimmed =
    typeof preferredWorkflowId === "string" ? preferredWorkflowId.trim() : "";
  if (trimmed && list.some((wf) => wf.id === trimmed)) {
    return {
      workflowId: trimmed,
      recommendedWorkflowId,
      fromUserPreference: true,
    };
  }
  return {
    workflowId: recommendedWorkflowId,
    recommendedWorkflowId,
    fromUserPreference: false,
  };
}

export function transferColabOneDigitalHumanFields(
  sourceWf: WorkflowTemplate | undefined,
  targetWf: WorkflowTemplate | undefined,
  currentValues: Record<string, string | number | boolean>,
  locale?: ColabOneLocale,
): Record<string, string | number | boolean> {
  if (!targetWf) {
    return {};
  }
  const next = defaultFieldValues(targetWf.fields || [], locale);
  const sourceFields = new Map<string, WorkflowField>();
  for (const field of sourceWf?.fields || []) {
    sourceFields.set(field.key, field);
  }
  const sourceSupportsRenderQuality =
    !sourceWf || sourceFields.has("pipeline.render_quality");
  const wasAutoSyncedNative720pLanczos =
    sourceSupportsRenderQuality &&
    hasOwn(currentValues, "pipeline.render_quality") &&
    hasOwn(currentValues, "pipeline.upscale_mode") &&
    String(currentValues["pipeline.render_quality"] ?? "").trim() ===
      "native_720p" &&
    String(currentValues["pipeline.upscale_mode"] ?? "").trim() === "lanczos";

  for (const targetField of targetWf.fields || []) {
    if (COLABONE_DIGITAL_HUMAN_ENGINE_SPECIFIC_KEYS.has(targetField.key)) {
      continue;
    }
    if (
      wasAutoSyncedNative720pLanczos &&
      targetField.key === "pipeline.upscale_mode"
    ) {
      continue;
    }
    if (!hasOwn(currentValues, targetField.key)) {
      continue;
    }
    const raw: unknown = currentValues[targetField.key];
    if (raw === undefined || raw === null) {
      continue;
    }
    const sourceField = sourceFields.get(targetField.key);
    if (sourceField && sourceField.field_type !== targetField.field_type) {
      continue;
    }

    if (targetField.field_type === "select") {
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
      // speaker IDs (e.g. Qwen3-TTS `qwen_*`) outside the static template options;
      // callers reconcile against the active TTS engine via `reconcileVoicePresetForEngine`.
      const allowsDynamicSelectOptions =
        targetField.key === "pipeline.voice_preset";
      if (
        !allowsDynamicSelectOptions &&
        Array.isArray(targetField.options) &&
        targetField.options.length > 0 &&
        !targetField.options.includes(candidate)
      ) {
        continue;
      }
      next[targetField.key] = candidate;
      continue;
    }

    if (targetField.field_type === "boolean") {
      if (typeof raw === "boolean") {
        next[targetField.key] = raw;
      } else if (typeof raw === "string") {
        const normalized = raw.trim().toLowerCase();
        if (normalized === "true" || normalized === "1") {
          next[targetField.key] = true;
        } else if (normalized === "false" || normalized === "0") {
          next[targetField.key] = false;
        }
      }
      continue;
    }

    if (
      targetField.field_type === "integer" ||
      targetField.field_type === "number"
    ) {
      if (typeof raw === "string" && raw.trim() === "") {
        next[targetField.key] = "";
        continue;
      }
      const parsed = parseFiniteNumber(raw);
      if (parsed === undefined) {
        continue;
      }
      let value =
        targetField.field_type === "integer"
          ? normalizeZero(Math.round(parsed))
          : normalizeZero(parsed);
      if (
        typeof targetField.min_value === "number" &&
        Number.isFinite(targetField.min_value) &&
        value < targetField.min_value
      ) {
        value =
          targetField.field_type === "integer"
            ? normalizeZero(Math.ceil(targetField.min_value))
            : normalizeZero(targetField.min_value);
      }
      if (
        typeof targetField.max_value === "number" &&
        Number.isFinite(targetField.max_value) &&
        value > targetField.max_value
      ) {
        value =
          targetField.field_type === "integer"
            ? normalizeZero(Math.floor(targetField.max_value))
            : normalizeZero(targetField.max_value);
      }
      next[targetField.key] = value;
      continue;
    }

    if (
      targetField.field_type === "text" ||
      targetField.field_type === "textarea"
    ) {
      if (
        typeof raw !== "string" &&
        typeof raw !== "boolean" &&
        !(typeof raw === "number" && Number.isFinite(raw))
      ) {
        continue;
      }
      const str = String(raw);
      if (
        typeof targetField.default === "string" &&
        isColabOnePresetVisualPrompt(targetField.default) &&
        isColabOnePresetVisualPrompt(str)
      ) {
        next[targetField.key] = t("comfy.defaultVisualPrompt", undefined, locale);
      } else {
        next[targetField.key] = str;
      }
      continue;
    }

    if (
      targetField.field_type === "image" ||
      targetField.field_type === "audio"
    ) {
      if (typeof raw === "string") {
        next[targetField.key] = raw;
      }
      continue;
    }

    if (typeof raw === "string" || typeof raw === "boolean") {
      next[targetField.key] = raw;
    } else if (typeof raw === "number" && Number.isFinite(raw)) {
      next[targetField.key] = normalizeZero(raw);
    }
  }

  return next;
}
