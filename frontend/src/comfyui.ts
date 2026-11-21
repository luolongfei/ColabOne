import { colabOneCommand } from "./api";
import { t } from "./i18n";
import type { WorkflowField } from "./types";

export type WorkflowImportResult =
  | { ok: true; workflow: Record<string, unknown> }
  | { ok: false; error: string };

const CHUNK_SIZE = 512 * 1024;
const MAX_OUTPUT_BYTES = 512 * 1024 ** 2;
const UNSAFE_PREVIEW_MIMES = new Set([
  "image/svg+xml",
  "text/html",
  "application/xhtml+xml",
  "text/javascript",
  "application/javascript",
]);

export const CANVAS_FORMAT_ERROR = t("comfy.errCanvasFormat", undefined, "zh-CN");

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function parseWorkflowImportJSON(raw: string): WorkflowImportResult {
  const trimmed = (raw || "").trim();
  if (!trimmed) {
    return { ok: false, error: t("comfy.errEmptyJson") };
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

  if (Array.isArray(parsed.nodes)) {
    return { ok: false, error: t("comfy.errCanvasFormat") };
  }

  const entries = Object.entries(parsed);
  if (entries.length === 0) {
    return { ok: false, error: t("comfy.errNoNodes") };
  }

  for (const [nodeId, nodeValue] of entries) {
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
    if (!isPlainRecord(nodeValue.inputs)) {
      return {
        ok: false,
        error: t("comfy.errMissingInputs", { id: nodeId }),
      };
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
      values[field.key] = hasDefault ? Boolean(field.default) : false;
    } else if (
      field.field_type === "integer" ||
      field.field_type === "number"
    ) {
      if (typeof field.default === "number" && Number.isFinite(field.default)) {
        values[field.key] = field.default;
      } else if (
        typeof field.default === "string" &&
        field.default.trim() !== "" &&
        Number.isFinite(Number(field.default))
      ) {
        values[field.key] = Number(field.default);
      } else if (
        typeof field.min_value === "number" &&
        Number.isFinite(field.min_value)
      ) {
        values[field.key] = field.min_value;
      } else {
        values[field.key] = 0;
      }
    } else if (field.field_type === "select") {
      if (hasDefault && String(field.default) !== "") {
        values[field.key] = String(field.default);
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

export function formatOutputSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  if (bytes < 1024) {
    return `${Math.round(bytes)} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function decodeBase64(data: string): Uint8Array {
  const binary = atob(data);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

export async function outputBlobURL(id: string): Promise<string> {
  if (!id || !id.trim()) {
    throw new Error(t("comfy.errInvalidOutputId"));
  }
  const chunks: Uint8Array[] = [];
  let offset = 0;
  let totalSize = -1;
  let expectedSha256 = "";
  let mime = "application/octet-stream";

  while (totalSize < 0 || offset < totalSize) {
    const part = await colabOneCommand("comfyui.output.asset", { id, offset });
    if (!part || typeof part.data !== "string") {
      throw new Error(t("comfy.errInvalidOutputTransfer"));
    }
    const decoded = decodeBase64(part.data);

    if (totalSize < 0) {
      const size = Number(part.size);
      if (!Number.isFinite(size) || size <= 0 || size > MAX_OUTPUT_BYTES) {
        throw new Error(t("comfy.errInvalidOutputSize"));
      }
      totalSize = size;
      if (typeof part.sha256 === "string" && part.sha256) {
        expectedSha256 = part.sha256;
      }
      if (typeof part.mime === "string" && part.mime.trim()) {
        const candidate = part.mime.trim().toLowerCase().split(";")[0];
        if (UNSAFE_PREVIEW_MIMES.has(candidate)) {
          throw new Error(t("comfy.errUnsupportedPreview"));
        }
        mime = candidate;
      }
    }

    if (
      (typeof part.offset === "number" && part.offset !== offset) ||
      (typeof part.size === "number" && part.size !== totalSize) ||
      (expectedSha256 &&
        typeof part.sha256 === "string" &&
        part.sha256 &&
        part.sha256 !== expectedSha256) ||
      !decoded.length ||
      decoded.length > CHUNK_SIZE ||
      offset + decoded.length > totalSize
    ) {
      throw new Error(t("comfy.errIncompleteTransfer"));
    }

    chunks.push(decoded);
    offset += decoded.length;
  }

  const blob = new Blob(chunks as BlobPart[], { type: mime });
  if (expectedSha256 && globalThis.crypto?.subtle) {
    const digest = await globalThis.crypto.subtle.digest(
      "SHA-256",
      await blob.arrayBuffer(),
    );
    const actual = Array.from(new Uint8Array(digest), (n) =>
      n.toString(16).padStart(2, "0"),
    ).join("");
    if (actual !== expectedSha256) {
      throw new Error(t("comfy.errOutputChecksum"));
    }
  }
  return URL.createObjectURL(blob);
}

export function extractPublicHost(url?: string): string {
  if (!url) return "";
  try {
    const parsed = new URL(url);
    return parsed.host;
  } catch {
    return url;
  }
}
