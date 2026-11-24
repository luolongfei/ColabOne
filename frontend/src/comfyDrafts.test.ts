import { afterEach, describe, expect, it, vi } from "vitest";
import {
  colabOneBase64ToBlob,
  createColabOneWorkflowDraftClient,
  hasColabOneDraftContent,
  mergeColabOneWorkflowDraftFields,
  sanitizeColabOneDraftFieldsForSave,
  type ColabOneWorkflowDraft,
  type ColabOneWorkflowDraftMediaMeta,
  type ColabOneWorkflowDraftMediaPayload,
} from "./comfyDrafts";
import { COLABONE_CANONICAL_VISUAL_PROMPT } from "./comfyui";
import { t } from "./i18n";
import { COLABONE_STORAGE_KEYS } from "./storage";
import type { WorkflowField } from "./types";

function makeField(overrides: Partial<WorkflowField> & { key: string }): WorkflowField {
  return {
    label: overrides.key,
    node_id: "1",
    input_name: overrides.key,
    field_type: "text",
    ...overrides,
  };
}

function createMemoryStorage(): Storage {
  const store = new Map<string, string>();
  return {
    get length() {
      return store.size;
    },
    clear() {
      store.clear();
    },
    getItem(key: string) {
      return store.has(key) ? store.get(key)! : null;
    },
    key(index: number) {
      return Array.from(store.keys())[index] ?? null;
    },
    removeItem(key: string) {
      store.delete(key);
    },
    setItem(key: string, value: string) {
      store.set(key, String(value));
    },
  };
}

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("mergeColabOneWorkflowDraftFields", () => {
  const fields: WorkflowField[] = [
    makeField({
      key: "pipeline.script_text",
      field_type: "textarea",
      default: "",
    }),
    makeField({
      key: "5.positive_prompt",
      field_type: "textarea",
      default: COLABONE_CANONICAL_VISUAL_PROMPT,
    }),
    makeField({
      key: "pipeline.tts_engine",
      field_type: "select",
      default: "voxcpm2",
      options: ["voxcpm2", "qwen3_tts", "indextts2"],
    }),
    makeField({
      key: "8.steps",
      field_type: "integer",
      default: 6,
      min_value: 1,
    }),
    makeField({
      key: "8.cfg",
      field_type: "number",
      default: 1.5,
    }),
    makeField({
      key: "8.enhance",
      field_type: "boolean",
      default: false,
    }),
    makeField({
      key: "1.image",
      field_type: "image",
      default: "",
    }),
    makeField({
      key: "2.audio",
      field_type: "audio",
      default: "",
    }),
  ];

  it("returns localized defaults when draftFields is undefined", () => {
    const merged = mergeColabOneWorkflowDraftFields(fields, undefined, "zh-CN");
    expect(merged).toEqual({
      "pipeline.script_text": "",
      "5.positive_prompt": t("comfy.defaultVisualPrompt", undefined, "zh-CN"),
      "pipeline.tts_engine": "voxcpm2",
      "8.steps": 6,
      "8.cfg": 1.5,
      "8.enhance": false,
      "1.image": "",
      "2.audio": "",
    });
  });

  it("restores edited text, numeric, boolean, select, and media fields backed by persisted media while ignoring ghost media filenames", () => {
    const validMedia: Record<string, ColabOneWorkflowDraftMediaMeta> = {
      "1.image": {
        id: "a".repeat(32),
        filename: "anchor.png",
        kind: "image",
        mime: "image/png",
        size: 1234,
        sha256: "b".repeat(64),
        updated_at: 1700000000,
      },
      "2.audio": {
        id: "c".repeat(32),
        filename: "voice.wav",
        kind: "audio",
        mime: "audio/wav",
        size: 5678,
        sha256: "d".repeat(64),
        updated_at: 1700000000,
      },
    };

    const merged = mergeColabOneWorkflowDraftFields(
      fields,
      {
        "pipeline.script_text": "大家好，这是一段测试口播。",
        "5.positive_prompt": "Custom cyberpunk studio lighting",
        "pipeline.tts_engine": "qwen3_tts",
        "8.steps": "9.6",
        "8.cfg": "2.25",
        "8.enhance": "true",
        "1.image": "stale-ghost.png",
        "2.audio": "stale-ghost.wav",
      },
      "zh-CN",
      validMedia,
    );

    expect(merged).toEqual({
      "pipeline.script_text": "大家好，这是一段测试口播。",
      "5.positive_prompt": "Custom cyberpunk studio lighting",
      "pipeline.tts_engine": "qwen3_tts",
      "8.steps": 10,
      "8.cfg": 2.25,
      "8.enhance": true,
      "1.image": "anchor.png",
      "2.audio": "voice.wav",
    });

    // Without validMedia backing, ghost filenames in draftFields must NOT be resurrected
    const withoutMedia = mergeColabOneWorkflowDraftFields(
      fields,
      {
        "pipeline.script_text": "保留文稿",
        "1.image": "deleted-on-disk.png",
        "2.audio": "deleted-on-disk.wav",
      },
      "zh-CN",
    );
    expect(withoutMedia["pipeline.script_text"]).toBe("保留文稿");
    expect(withoutMedia["1.image"]).toBe("");
    expect(withoutMedia["2.audio"]).toBe("");
  });

  it("re-localizes untouched preset visual prompt on locale change while preserving custom prompts and empty string", () => {
    // Saved under zh-CN preset, restored under en-US -> localized to en-US
    const restoredEn = mergeColabOneWorkflowDraftFields(
      fields,
      {
        "5.positive_prompt": t("comfy.defaultVisualPrompt", undefined, "zh-CN"),
      },
      "en-US",
    );
    expect(restoredEn["5.positive_prompt"]).toBe(
      t("comfy.defaultVisualPrompt", undefined, "en-US"),
    );

    // Saved with canonical English preset, restored under ja-JP -> localized to ja-JP
    const restoredJa = mergeColabOneWorkflowDraftFields(
      fields,
      {
        "5.positive_prompt": COLABONE_CANONICAL_VISUAL_PROMPT,
      },
      "ja-JP",
    );
    expect(restoredJa["5.positive_prompt"]).toBe(
      t("comfy.defaultVisualPrompt", undefined, "ja-JP"),
    );

    // Explicitly cleared empty string is preserved verbatim
    const restoredCleared = mergeColabOneWorkflowDraftFields(
      fields,
      {
        "5.positive_prompt": "",
      },
      "ko-KR",
    );
    expect(restoredCleared["5.positive_prompt"]).toBe("");

    // Custom visual prompt is preserved verbatim
    const restoredCustom = mergeColabOneWorkflowDraftFields(
      fields,
      {
        "5.positive_prompt": "Warm sunset studio portrait",
      },
      "zh-TW",
    );
    expect(restoredCustom["5.positive_prompt"]).toBe(
      "Warm sunset studio portrait",
    );
  });

  it("keeps empty string for cleared numeric fields and ignores unknown keys and invalid values", () => {
    const merged = mergeColabOneWorkflowDraftFields(
      fields,
      {
        unknown_key: "should not appear",
        "8.steps": "",
        "8.cfg": Number.NaN,
        "8.enhance": "not-a-bool",
        "pipeline.script_text": { bad: true } as unknown as string,
      },
      "en-US",
    );

    expect("unknown_key" in merged).toBe(false);
    expect(merged["8.steps"]).toBe("");
    expect(merged["8.cfg"]).toBe(1.5);
    expect(merged["8.enhance"]).toBe(false);
    expect(merged["pipeline.script_text"]).toBe("");

    // Additional invalid numeric/boolean values fall back to defaults
    const merged2 = mergeColabOneWorkflowDraftFields(
      fields,
      {
        "8.steps": "abc",
        "8.cfg": Number.POSITIVE_INFINITY,
        "8.enhance": 123 as unknown as boolean,
      },
      "en-US",
    );
    expect(merged2["8.steps"]).toBe(6);
    expect(merged2["8.cfg"]).toBe(1.5);
    expect(merged2["8.enhance"]).toBe(false);

    // Negative zero normalized to +0
    const mergedNegZero = mergeColabOneWorkflowDraftFields(
      fields,
      { "8.steps": -0.1, "8.cfg": -0 },
      "en-US",
    );
    expect(Object.is(mergedNegZero["8.steps"], 0)).toBe(true);
    expect(Object.is(mergedNegZero["8.cfg"], 0)).toBe(true);
  });
});

describe("hasColabOneDraftContent", () => {
  const fields: WorkflowField[] = [
    makeField({
      key: "pipeline.script_text",
      field_type: "textarea",
      default: "",
    }),
    makeField({
      key: "5.positive_prompt",
      field_type: "textarea",
      default: COLABONE_CANONICAL_VISUAL_PROMPT,
    }),
    makeField({
      key: "8.steps",
      field_type: "integer",
      default: 6,
    }),
    makeField({
      key: "8.enhance",
      field_type: "boolean",
      default: false,
    }),
  ];

  it("returns false for exact defaults and locale-equivalent preset visual prompts", () => {
    const zhDefaults = {
      "pipeline.script_text": "",
      "5.positive_prompt": t("comfy.defaultVisualPrompt", undefined, "zh-CN"),
      "8.steps": 6,
      "8.enhance": false,
    };

    expect(hasColabOneDraftContent(fields, zhDefaults, [], "zh-CN")).toBe(
      false,
    );
    // Different locale's preset visual prompt is still treated as equivalent to default
    expect(hasColabOneDraftContent(fields, zhDefaults, [], "en-US")).toBe(
      false,
    );
    expect(hasColabOneDraftContent(fields, zhDefaults, [], "ja-JP")).toBe(
      false,
    );
  });

  it("returns true for modified text, cleared preset prompt, modified numeric/boolean, or attached media", () => {
    const defaults = {
      "pipeline.script_text": "",
      "5.positive_prompt": t("comfy.defaultVisualPrompt", undefined, "en-US"),
      "8.steps": 6,
      "8.enhance": false,
    };

    expect(
      hasColabOneDraftContent(
        fields,
        { ...defaults, "pipeline.script_text": "hello" },
        [],
        "en-US",
      ),
    ).toBe(true);

    expect(
      hasColabOneDraftContent(
        fields,
        { ...defaults, "5.positive_prompt": "" },
        [],
        "en-US",
      ),
    ).toBe(true);

    expect(
      hasColabOneDraftContent(
        fields,
        { ...defaults, "5.positive_prompt": "custom prompt" },
        [],
        "en-US",
      ),
    ).toBe(true);

    expect(
      hasColabOneDraftContent(
        fields,
        { ...defaults, "8.steps": 12 },
        [],
        "en-US",
      ),
    ).toBe(true);

    expect(
      hasColabOneDraftContent(
        fields,
        { ...defaults, "8.enhance": true },
        [],
        "en-US",
      ),
    ).toBe(true);

    // Attached media counts as draft content even when all text fields are defaults
    expect(
      hasColabOneDraftContent(fields, defaults, ["1.image"], "en-US"),
    ).toBe(true);
  });

  it("migrates exact pre-optimization LongCat defaults when restoring legacy drafts while preserving custom values and post-upgrade choices", () => {
    const longCatFields: WorkflowField[] = [
      makeField({
        key: "pipeline.script_text",
        field_type: "textarea",
        default: "",
      }),
      makeField({
        key: "pipeline.render_quality",
        field_type: "select",
        default: "balanced_480p_ai2x",
      }),
      makeField({
        key: "pipeline.upscale_mode",
        field_type: "select",
        default: "ai_2x",
      }),
      makeField({
        key: "pipeline.max_segment_seconds",
        field_type: "number",
        default: 3.7,
      }),
      makeField({
        key: "14.steps",
        field_type: "integer",
        default: 8,
      }),
      makeField({
        key: "5.positive_prompt",
        field_type: "textarea",
        default: COLABONE_CANONICAL_VISUAL_PROMPT,
      }),
    ];

    // 1. Pre-optimization saved draft (no pipeline.render_quality key) with old untouched defaults
    const migrated = mergeColabOneWorkflowDraftFields(
      longCatFields,
      {
        "pipeline.script_text": "保留我的播报文稿",
        "pipeline.upscale_mode": "none",
        "pipeline.max_segment_seconds": 9,
        "14.steps": 12,
        "5.positive_prompt":
          "清晰演播室人像镜头，主播面向镜头自然说话，面部表情生动自然，头部轻微摆动，自然眨眼，焦距锐利，柔和演播室灯光，背景稳定不抖动",
      },
      "zh-CN",
    );
    expect(migrated["pipeline.script_text"]).toBe("保留我的播报文稿");
    expect(migrated["pipeline.render_quality"]).toBe("balanced_480p_ai2x");
    expect(migrated["pipeline.upscale_mode"]).toBe("ai_2x");
    expect(migrated["pipeline.max_segment_seconds"]).toBe(3.7);
    expect(migrated["14.steps"]).toBe(8);
    expect(migrated["5.positive_prompt"]).toBe(t("comfy.defaultVisualPrompt", undefined, "zh-CN"));

    // 2. Pre-optimization saved draft with user-customized non-default values keeps custom values
    const legacyCustom = mergeColabOneWorkflowDraftFields(
      longCatFields,
      {
        "pipeline.upscale_mode": "lanczos",
        "pipeline.max_segment_seconds": 6,
        "14.steps": 16,
      },
      "zh-CN",
    );
    expect(legacyCustom["pipeline.upscale_mode"]).toBe("lanczos");
    expect(legacyCustom["pipeline.max_segment_seconds"]).toBe(6);
    expect(legacyCustom["14.steps"]).toBe(16);

    // 3. Post-optimization saved draft (contains pipeline.render_quality) preserves explicit none/9/12
    const postUpgrade = mergeColabOneWorkflowDraftFields(
      longCatFields,
      {
        "pipeline.render_quality": "native_720p",
        "pipeline.upscale_mode": "none",
        "pipeline.max_segment_seconds": 9,
        "14.steps": 12,
      },
      "zh-CN",
    );
    expect(postUpgrade["pipeline.render_quality"]).toBe("native_720p");
    expect(postUpgrade["pipeline.upscale_mode"]).toBe("none");
    expect(postUpgrade["pipeline.max_segment_seconds"]).toBe(9);
    expect(postUpgrade["14.steps"]).toBe(12);
  });
});

describe("sanitizeColabOneDraftFieldsForSave", () => {
  const fields: WorkflowField[] = [
    makeField({ key: "a.text", field_type: "text" }),
    makeField({ key: "b.num", field_type: "number" }),
    makeField({ key: "c.bool", field_type: "boolean" }),
    makeField({ key: "d.image", field_type: "image" }),
  ];

  it("keeps only valid primitive field entries and strips non-finite numbers and unknown keys", () => {
    const result = sanitizeColabOneDraftFieldsForSave(fields, {
      "a.text": "hello",
      "b.num": Number.NaN,
      "c.bool": true,
      "d.image": "portrait.png",
      extra: "ignored",
    });

    expect(result).toEqual({
      "a.text": "hello",
      "c.bool": true,
      "d.image": "portrait.png",
    });

    const finiteResult = sanitizeColabOneDraftFieldsForSave(fields, {
      "a.text": "",
      "b.num": -0,
      "c.bool": false,
      "d.image": "",
    });
    expect(finiteResult).toEqual({
      "a.text": "",
      "b.num": 0,
      "c.bool": false,
      "d.image": "",
    });
    expect(Object.is(finiteResult["b.num"], 0)).toBe(true);
  });
});

describe("colabOneBase64ToBlob", () => {
  it("decodes UTF-8 text and arbitrary binary bytes with accurate MIME type", async () => {
    const textBlob = colabOneBase64ToBlob(btoa("hello draft media"), "text/plain");
    expect(textBlob.type).toBe("text/plain");
    expect(textBlob.size).toBe(17);
    expect(await textBlob.text()).toBe("hello draft media");

    // Full binary byte range 0x00..0xff
    const rawBytes = new Uint8Array(256);
    for (let i = 0; i < 256; i += 1) {
      rawBytes[i] = i;
    }
    let binaryStr = "";
    for (let i = 0; i < rawBytes.length; i += 1) {
      binaryStr += String.fromCharCode(rawBytes[i]);
    }
    const base64 = btoa(binaryStr);
    const binBlob = colabOneBase64ToBlob(base64, "image/png");
    expect(binBlob.type).toBe("image/png");
    expect(binBlob.size).toBe(256);
    const decoded = new Uint8Array(await binBlob.arrayBuffer());
    expect(Array.from(decoded)).toEqual(Array.from(rawBytes));

    // Empty MIME falls back to application/octet-stream
    const fallbackMime = colabOneBase64ToBlob(btoa("ok"), "");
    expect(fallbackMime.type).toBe("application/octet-stream");
  });

  it("throws on malformed base64", () => {
    expect(() => colabOneBase64ToBlob("!!!bad-base64!!!", "image/png")).toThrow();
  });
});

describe("createColabOneWorkflowDraftClient", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("serializes writes per workflow so a slow save can never overwrite a subsequent clear", async () => {
    const storage = createMemoryStorage();
    const events: string[] = [];
    const saveDeferred = createDeferred<{ ok: true; draft: ColabOneWorkflowDraft }>();
    let backendDraft: ColabOneWorkflowDraft | null = null;

    const invoke = vi.fn(async (action: string, payload?: Record<string, unknown>) => {
      events.push(`start:${action}:${String(payload?.workflow_id ?? "")}`);
      if (action === "comfyui.draft.save") {
        const res = await saveDeferred.promise;
        backendDraft = res.draft;
        events.push(`end:${action}`);
        return res;
      }
      if (action === "comfyui.draft.clear") {
        backendDraft = null;
        events.push(`end:${action}`);
        return { ok: true };
      }
      if (action === "comfyui.draft.get") {
        events.push(`end:${action}`);
        return { draft: backendDraft };
      }
      throw new Error(`unexpected action ${action}`);
    });

    const client = createColabOneWorkflowDraftClient(invoke, storage);

    const savePromise = client.saveColabOneWorkflowDraft("wf-1", {
      "pipeline.script_text": "stale draft text",
    });
    const clearPromise = client.clearColabOneWorkflowDraft("wf-1");

    // Yield microtasks so save starts, while clear MUST still be waiting behind save
    await Promise.resolve();
    await Promise.resolve();
    expect(events).toEqual(["start:comfyui.draft.save:wf-1"]);

    // Resolve slow save -> clear should execute right after
    saveDeferred.resolve({
      ok: true,
      draft: {
        workflow_id: "wf-1",
        fields: { "pipeline.script_text": "stale draft text" },
        media: {},
        updated_at: 1_700_000_001,
      },
    });

    await Promise.all([savePromise, clearPromise]);

    expect(events).toEqual([
      "start:comfyui.draft.save:wf-1",
      "end:comfyui.draft.save",
      "start:comfyui.draft.clear:wf-1",
      "end:comfyui.draft.clear",
    ]);
    expect(backendDraft).toBeNull();
    // LocalStorage mirror must also be cleared after clear completes
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).toBeNull();

    // Subsequent load sees null
    await expect(client.loadColabOneWorkflowDraft("wf-1")).resolves.toBeNull();
  });

  it("allows independent workflows to write concurrently and recovers queue after a failed write", async () => {
    const storage = createMemoryStorage();
    const wf1Deferred = createDeferred<{ ok: true; draft: ColabOneWorkflowDraft }>();
    const order: string[] = [];

    const invoke = vi.fn(async (action: string, payload?: Record<string, unknown>) => {
      const wfId = String(payload?.workflow_id ?? "");
      if (action === "comfyui.draft.save" && wfId === "wf-1") {
        order.push("start:wf-1");
        return wf1Deferred.promise;
      }
      if (action === "comfyui.draft.save" && wfId === "wf-2") {
        order.push("done:wf-2");
        return {
          ok: true,
          draft: {
            workflow_id: "wf-2",
            fields: payload?.fields ?? {},
            media: {},
            updated_at: 2,
          },
        };
      }
      if (action === "comfyui.draft.clear" && wfId === "wf-1") {
        order.push("done:clear-wf-1");
        return { ok: true };
      }
      return { ok: true };
    });

    const client = createColabOneWorkflowDraftClient(invoke, storage);

    const p1 = client.saveColabOneWorkflowDraft("wf-1", { a: "1" });
    const p2 = client.saveColabOneWorkflowDraft("wf-2", { b: "2" });
    const p3 = client.clearColabOneWorkflowDraft("wf-1");

    // wf-2 finishes without waiting for wf-1
    await p2;
    expect(order).toContain("done:wf-2");
    expect(order).not.toContain("done:clear-wf-1");

    // Reject wf-1 save with real backend error -> p1 rejects, but p3 (clear) still runs and succeeds!
    wf1Deferred.reject(new Error("disk full"));
    await expect(p1).rejects.toThrow("disk full");
    await expect(p3).resolves.toBeUndefined();
    expect(order[order.length - 1]).toBe("done:clear-wf-1");
  });

  it("supports backend RPC lifecycle for fields and media without leaking media into localStorage", async () => {
    const storage = createMemoryStorage();
    const mediaMeta: ColabOneWorkflowDraftMediaMeta = {
      id: "media-1",
      filename: "portrait.png",
      kind: "image",
      mime: "image/png",
      size: 4,
      sha256: "a".repeat(64),
      updated_at: 1_700_000_010,
    };
    const mediaPayload: ColabOneWorkflowDraftMediaPayload = {
      ...mediaMeta,
      data: btoa("PNG!"),
    };

    const invoke = vi.fn(async (action: string, payload?: Record<string, unknown>) => {
      if (action === "comfyui.draft.save") {
        return {
          ok: true,
          draft: {
            workflow_id: payload?.workflow_id,
            fields: payload?.fields,
            media: {},
            updated_at: 1_700_000_005,
          },
        };
      }
      if (action === "comfyui.draft.media.save") {
        return {
          ok: true,
          media: mediaMeta,
          draft: {
            workflow_id: payload?.workflow_id,
            fields: { "pipeline.script_text": "hello" },
            media: { "1.image": mediaMeta },
            updated_at: 1_700_000_010,
          },
        };
      }
      if (action === "comfyui.draft.media.read") {
        return {
          id: mediaPayload.id,
          filename: mediaPayload.filename,
          kind: mediaPayload.kind,
          mime: mediaPayload.mime,
          size: mediaPayload.size,
          sha256: mediaPayload.sha256,
          data: mediaPayload.data,
        };
      }
      if (action === "comfyui.draft.media.delete") {
        return { ok: true };
      }
      if (action === "comfyui.draft.get") {
        return {
          draft: {
            workflow_id: payload?.workflow_id,
            fields: { "pipeline.script_text": "hello" },
            media: { "1.image": mediaMeta },
            updated_at: 1_700_000_010,
          },
        };
      }
      throw new Error(`unexpected ${action}`);
    });

    const client = createColabOneWorkflowDraftClient(invoke, storage);

    const saved = await client.saveColabOneWorkflowDraft("wf-dh", {
      "pipeline.script_text": "hello",
    });
    expect(saved?.fields).toEqual({ "pipeline.script_text": "hello" });

    const savedMedia = await client.saveColabOneWorkflowDraftMedia(
      "wf-dh",
      "1.image",
      { filename: "portrait.png", kind: "image", data: btoa("PNG!") },
    );
    expect(savedMedia).toEqual(mediaMeta);
    expect(invoke).toHaveBeenCalledWith("comfyui.draft.media.save", {
      workflow_id: "wf-dh",
      field_key: "1.image",
      filename: "portrait.png",
      kind: "image",
      data: btoa("PNG!"),
    });

    const readMedia = await client.readColabOneWorkflowDraftMedia(
      "wf-dh",
      "1.image",
    );
    expect(readMedia).toEqual({ ...mediaPayload, updated_at: 0 });

    const loaded = await client.loadColabOneWorkflowDraft("wf-dh");
    expect(loaded?.media["1.image"]).toEqual(mediaMeta);

    await client.deleteColabOneWorkflowDraftMedia("wf-dh", "1.image");
    expect(invoke).toHaveBeenCalledWith("comfyui.draft.media.delete", {
      workflow_id: "wf-dh",
      field_key: "1.image",
    });

    // LocalStorage mirror only contains lightweight text fields, never base64 media data
    const rawMirror = storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts) || "";
    expect(rawMirror).toContain("hello");
    expect(rawMirror).not.toContain(btoa("PNG!"));
  });

  it("falls back to localStorage when desktop bridge is unavailable and never resurrects stale localStorage when backend returns null", async () => {
    const storage = createMemoryStorage();

    // 1. Offline mode (bridge unavailable) saves and loads text fields via localStorage
    const offlineInvoke = vi.fn(async () => {
      throw new Error(
        "mygo: runtime not found; is this page running inside a MyGo window?",
      );
    });
    const offlineClient = createColabOneWorkflowDraftClient(
      offlineInvoke,
      storage,
    );

    const offlineSaved = await offlineClient.saveColabOneWorkflowDraft(
      "wf-offline",
      {
        "pipeline.script_text": "offline script",
        "8.steps": 8,
      },
    );
    expect(offlineSaved).not.toBeNull();
    expect(offlineSaved?.fields).toEqual({
      "pipeline.script_text": "offline script",
      "8.steps": 8,
    });
    expect(offlineSaved?.media).toEqual({});

    const offlineLoaded =
      await offlineClient.loadColabOneWorkflowDraft("wf-offline");
    expect(offlineLoaded?.fields).toEqual({
      "pipeline.script_text": "offline script",
      "8.steps": 8,
    });

    // Media operations degrade cleanly to null / no-op offline
    await expect(
      offlineClient.saveColabOneWorkflowDraftMedia("wf-offline", "1.image", {
        filename: "p.png",
        kind: "image",
        data: btoa("abc"),
      }),
    ).resolves.toBeNull();
    await expect(
      offlineClient.readColabOneWorkflowDraftMedia("wf-offline", "1.image"),
    ).resolves.toBeNull();
    await expect(
      offlineClient.deleteColabOneWorkflowDraftMedia("wf-offline", "1.image"),
    ).resolves.toBeUndefined();

    // 2. When backend IS available and explicitly returns { draft: null }, do NOT resurrect stale localStorage!
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).not.toBeNull();
    const onlineInvoke = vi.fn(async (action: string) => {
      if (action === "comfyui.draft.get") {
        return { draft: null };
      }
      return { ok: true };
    });
    const onlineClient = createColabOneWorkflowDraftClient(
      onlineInvoke,
      storage,
    );

    await expect(
      onlineClient.loadColabOneWorkflowDraft("wf-offline"),
    ).resolves.toBeNull();

    // 3. Clearing in offline mode deletes the localStorage entry
    await offlineClient.clearColabOneWorkflowDraft("wf-offline");
    expect(
      await offlineClient.loadColabOneWorkflowDraft("wf-offline"),
    ).toBeNull();
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).toBeNull();
  });

  it("propagates real backend errors while writing a localStorage safety net on text save", async () => {
    const storage = createMemoryStorage();
    const failingInvoke = vi.fn(async () => {
      throw new Error("permission denied");
    });

    const client = createColabOneWorkflowDraftClient(failingInvoke, storage, {
      allowLocalFallback: false,
    });

    await expect(
      client.saveColabOneWorkflowDraft("wf-err", {
        "pipeline.script_text": "important draft",
      }),
    ).rejects.toThrow("permission denied");

    // Safety-net localStorage mirror was still updated
    const stored = JSON.parse(
      storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts) || "{}",
    );
    expect(stored["wf-err"]?.fields).toEqual({
      "pipeline.script_text": "important draft",
    });

    // Media and load errors also propagate cleanly
    await expect(
      client.saveColabOneWorkflowDraftMedia("wf-err", "1.image", {
        filename: "a.png",
        kind: "image",
        data: btoa("x"),
      }),
    ).rejects.toThrow("permission denied");
    await expect(
      client.readColabOneWorkflowDraftMedia("wf-err", "1.image"),
    ).rejects.toThrow("permission denied");
    await expect(
      client.loadColabOneWorkflowDraft("wf-err"),
    ).rejects.toThrow("permission denied");
  });

  it("dispatches idle-queue writes synchronously for beforeunload safety while still serializing subsequent writes", async () => {
    const storage = createMemoryStorage();
    const first = createDeferred<any>();
    const calls: string[] = [];

    const invoke = vi.fn((action: string) => {
      calls.push(action);
      if (action === "comfyui.draft.save") {
        return first.promise;
      }
      return Promise.resolve({ ok: true });
    });

    const client = createColabOneWorkflowDraftClient(invoke, storage);

    const savePromise = client.saveColabOneWorkflowDraft("wf-sync-dispatch", {
      "pipeline.script_text": "before unload edit",
    });
    // Must be dispatched synchronously before any microtask flush!
    expect(calls).toEqual(["comfyui.draft.save"]);

    // Subsequent clear must still wait for the in-flight save to settle.
    const clearPromise = client.clearColabOneWorkflowDraft("wf-sync-dispatch");
    expect(calls).toEqual(["comfyui.draft.save"]);

    first.resolve({
      ok: true,
      draft: {
        workflow_id: "wf-sync-dispatch",
        fields: { "pipeline.script_text": "before unload edit" },
        media: {},
        updated_at: 1_700_000_000,
      },
    });
    await Promise.all([savePromise, clearPromise]);
    expect(calls).toEqual(["comfyui.draft.save", "comfyui.draft.clear"]);
  });
});
