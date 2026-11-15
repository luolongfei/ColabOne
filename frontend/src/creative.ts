import { command } from "./api";
export interface CreativeParams {
  model_id: string;
  kind: "image" | "video";
  prompt: string;
  negative_prompt: string;
  width: number;
  height: number;
  steps: number;
  count: number;
  seed: number;
  strength: number;
  reference_id: string;
  frames: number;
  fps: number;
}
export interface Creation {
  id: string;
  kind: "image" | "video";
  model: string;
  created_at: number;
  params: CreativeParams;
  size: number;
  sha256: string;
  extension: string;
  mime: string;
  elapsed: number;
  frame_count?: number;
  synced: boolean;
}
export async function creationURL(item: Creation) {
  if (!["image/png", "video/webm"].includes(item.mime))
    throw Error("作品格式不合法");
  const chunks: Uint8Array[] = [];
  let offset = 0;
  if (item.size > 64 * 1024 ** 2 || item.size <= 0)
    throw Error("作品大小不合法");
  while (offset < item.size) {
    const part = await command("creative.asset", { id: item.id, offset });
    const decoded = Uint8Array.from(atob(part.data), (c) => c.charCodeAt(0));
    if (
      part.offset !== offset ||
      part.size !== item.size ||
      part.sha256 !== item.sha256 ||
      !decoded.length ||
      decoded.length > 512 * 1024 ||
      offset + decoded.length > item.size
    )
      throw Error("作品传输不完整，请重试");
    chunks.push(decoded);
    offset += decoded.length;
  }
  const blob = new Blob(chunks as BlobPart[], { type: item.mime });
  if (crypto.subtle) {
    const digest = await crypto.subtle.digest(
      "SHA-256",
      await blob.arrayBuffer(),
    );
    const actual = Array.from(new Uint8Array(digest), (n) =>
      n.toString(16).padStart(2, "0"),
    ).join("");
    if (actual !== item.sha256) throw Error("作品校验失败，请重试");
  }
  return URL.createObjectURL(blob);
}

// Navigation drafts stay in this UI session, never in local persistent storage.
export const studioDrafts: Partial<
  Record<
    "image" | "video",
    {
      params: CreativeParams;
      ratio: string;
      resolution: string;
      seconds: number;
      referenceName: string;
      runtimeId?: string;
    }
  >
> = {};
