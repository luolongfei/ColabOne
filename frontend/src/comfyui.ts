import { colabOneCommand } from "./api";
import { t } from "./i18n";
import type {
  ColabOneComfyTtsEngineSpec,
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

export function defaultFieldValues(
  fields: WorkflowField[],
): Record<string, string | number | boolean> {
  const values: Record<string, string | number | boolean> = {};
  for (const field of fields || []) {
    const hasDefault = field.default !== undefined && field.default !== null;
    if (field.field_type === "boolean") {
      values[field.key] = hasDefault
        ? parseBooleanValue(field.default, false)
        : false;
    } else if (
      field.field_type === "integer" ||
      field.field_type === "number"
    ) {
      values[field.key] = resolveNumericFieldValue(field.default, field);
    } else if (field.field_type === "select") {
      const trimmedDefault = hasDefault ? String(field.default).trim() : "";
      if (trimmedDefault !== "") {
        values[field.key] = trimmedDefault;
      } else if (field.options && field.options.length > 0) {
        values[field.key] = field.options[0];
      } else {
        values[field.key] = "";
      }
    } else {
      values[field.key] = hasDefault ? String(field.default) : "";
    }
  }
  return values;
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

export function hasNonCommercialNotice(
  wf: WorkflowTemplate | undefined,
  selectedTts: ColabOneComfyTtsEngineSpec | undefined,
): NonCommercialNotice | null {
  const wfRestricted = wf?.commercial_ok === false;
  const ttsRestricted = selectedTts?.commercial_ok === false;
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

export function missingRequiredWorkflowFields(
  wf: WorkflowTemplate | undefined,
  values: Record<string, string | number | boolean>,
  uploadedInputIds: Record<string, string>,
  pendingUploads: Record<string, unknown>,
): WorkflowField[] {
  if (!wf?.fields?.length) {
    return [];
  }

  const rawTtsEngine = hasOwn(values, "pipeline.tts_engine")
    ? values["pipeline.tts_engine"]
    : undefined;
  const resolvedTtsEngineId =
    typeof rawTtsEngine === "string" && rawTtsEngine.trim()
      ? rawTtsEngine.trim()
      : (wf.pipeline?.default_tts_engine || "").trim();
  const requiresVoiceRefAudio =
    wf.pipeline?.kind === "digital_human" &&
    String(values?.["pipeline.audio_source"] ?? "tts").trim() === "tts" &&
    resolvedTtsEngineId === "indextts2";

  const missing: WorkflowField[] = [];
  for (const field of wf.fields) {
    const isRequired =
      Boolean(field.required) ||
      (requiresVoiceRefAudio && field.key === "pipeline.voice_ref_audio");
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
