import { afterEach, describe, expect, it, vi } from "vitest";
import {
  COLABONE_DRAFT_STATUS_VISIBLE_MS,
  colabOneBase64ToBlob,
  createColabOneDraftStatusController,
  createColabOneWorkflowDraftClient,
  hasColabOneDraftContent,
  mergeColabOneWorkflowDraftFields,
  sanitizeColabOneDraftFieldsForSave,
  type ColabOneWorkflowDraft,
  type ColabOneWorkflowDraftMediaMeta,
  type ColabOneWorkflowDraftMediaPayload,
  type ColabOneWorkflowDraftStatus,
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

    // Negative zero normalized to +0, while min_value bounds still clamp rounded integers
    const fieldsWithZeroMin = [
      ...fields,
      makeField({
        key: "8.seed",
        field_type: "integer",
        default: 42,
        min_value: 0,
      }),
    ];
    const mergedNegZero = mergeColabOneWorkflowDraftFields(
      fieldsWithZeroMin,
      { "8.steps": -0.1, "8.seed": -0.1, "8.cfg": -0 },
      "en-US",
    );
    expect(mergedNegZero["8.steps"]).toBe(1);
    expect(Object.is(mergedNegZero["8.seed"], 0)).toBe(true);
    expect(Object.is(mergedNegZero["8.cfg"], 0)).toBe(true);
  });

  it("normalizes '1'/'0' and case-insensitive boolean strings cleanly while rejecting invalid booleans", () => {
    const boolFields: WorkflowField[] = [
      makeField({ key: "b.true_one", field_type: "boolean", default: false }),
      makeField({ key: "b.false_zero", field_type: "boolean", default: true }),
      makeField({ key: "b.trimmed_true", field_type: "boolean", default: false }),
      makeField({ key: "b.trimmed_false", field_type: "boolean", default: true }),
      makeField({ key: "b.invalid_str", field_type: "boolean", default: true }),
      makeField({ key: "b.invalid_num", field_type: "boolean", default: true }),
    ];

    const merged = mergeColabOneWorkflowDraftFields(
      boolFields,
      {
        "b.true_one": "1",
        "b.false_zero": "0",
        "b.trimmed_true": "  TRUE  ",
        "b.trimmed_false": "  False ",
        "b.invalid_str": "2",
        "b.invalid_num": 0 as unknown as boolean,
      },
      "en-US",
    );

    expect(merged["b.true_one"]).toBe(true);
    expect(merged["b.false_zero"]).toBe(false);
    expect(merged["b.trimmed_true"]).toBe(true);
    expect(merged["b.trimmed_false"]).toBe(false);
    expect(merged["b.invalid_str"]).toBe(true);
    expect(merged["b.invalid_num"]).toBe(true);
  });

  it("sanitizes corrupted LongCat 1.6 drafts with NaN, booleans, out-of-range numbers, and unknown selects", () => {
    const longCat16Fields: WorkflowField[] = [
      makeField({
        key: "pipeline.quality_preset",
        field_type: "select",
        default: "cinema_flagship",
        options: ["cinema_flagship", "balanced_hd", "longform_fast"],
      }),
      makeField({
        key: "pipeline.framing_style",
        field_type: "select",
        default: "half_body_gestures",
        options: ["half_body_gestures", "closeup_presenter", "full_body_dynamic"],
      }),
      makeField({
        key: "pipeline.upscale_mode",
        field_type: "select",
        default: "auto",
        options: ["auto", "seedvr2_7b_sharp", "seedvr2_7b", "flashvsr_plus", "none"],
      }),
      makeField({
        key: "pipeline.target_resolution",
        field_type: "select",
        default: "1920x1080",
        options: ["1920x1080", "1080x1920", "2560x1440", "1440x2560", "1280x720", "720x1280"],
      }),
      makeField({
        key: "pipeline.sampling_mode",
        field_type: "select",
        default: "auto",
        options: ["auto", "non_distill_cfg", "distill_lora_mix"],
      }),
      makeField({
        key: "pipeline.voice_preset",
        field_type: "select",
        default: "shen_zhiyi",
        options: ["shen_zhiyi", "lin_xiaoman", "custom"],
      }),
      makeField({
        key: "pipeline.num_inference_steps",
        field_type: "integer",
        default: 50,
        min_value: 8,
        max_value: 60,
      }),
      makeField({
        key: "pipeline.audio_guidance_scale",
        field_type: "number",
        default: 4.0,
        min_value: 1.0,
        max_value: 7.0,
      }),
      makeField({
        key: "pipeline.text_guidance_scale",
        field_type: "number",
        default: 4.0,
        min_value: 1.0,
        max_value: 7.0,
      }),
      makeField({
        key: "pipeline.mix_steps",
        field_type: "integer",
        default: 4,
        min_value: 0,
        max_value: 8,
      }),
      makeField({
        key: "pipeline.mix_alpha",
        field_type: "number",
        default: 1.0,
        min_value: 0.0,
        max_value: 1.0,
      }),
      makeField({
        key: "pipeline.ref_img_index",
        field_type: "integer",
        default: 10,
        min_value: 0,
        max_value: 30,
      }),
      makeField({
        key: "pipeline.seed",
        field_type: "integer",
        default: 42,
        min_value: 0,
        max_value: 2147483647,
      }),
      makeField({
        key: "pipeline.crf",
        field_type: "integer",
        default: 16,
        min_value: 12,
        max_value: 24,
      }),
    ];

    // Corrupted NaN, boolean, and unknown select values fall back cleanly to defaults
    const corrupted = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.quality_preset": "ultra_8k_invalid",
        "pipeline.framing_style": true as unknown as string,
        "pipeline.upscale_mode": Number.NaN as unknown as string,
        "pipeline.target_resolution": "4096x2160",
        "pipeline.sampling_mode": "turbo_fast",
        "pipeline.voice_preset": "../bad preset!",
        "pipeline.num_inference_steps": Number.NaN,
        "pipeline.audio_guidance_scale": true as unknown as number,
        "pipeline.text_guidance_scale": Number.POSITIVE_INFINITY,
        "pipeline.mix_steps": false as unknown as number,
        "pipeline.mix_alpha": "not-a-number" as unknown as number,
        "pipeline.ref_img_index": Number.NEGATIVE_INFINITY,
        "pipeline.seed": true as unknown as number,
        "pipeline.crf": Number.NaN,
      },
      "zh-CN",
    );

    expect(corrupted["pipeline.quality_preset"]).toBe("cinema_flagship");
    expect(corrupted["pipeline.framing_style"]).toBe("half_body_gestures");
    expect(corrupted["pipeline.upscale_mode"]).toBe("auto");
    expect(corrupted["pipeline.target_resolution"]).toBe("1920x1080");
    expect(corrupted["pipeline.sampling_mode"]).toBe("auto");
    expect(corrupted["pipeline.voice_preset"]).toBe("shen_zhiyi");
    expect(corrupted["pipeline.num_inference_steps"]).toBe(50);
    expect(corrupted["pipeline.audio_guidance_scale"]).toBe(4.0);
    expect(corrupted["pipeline.text_guidance_scale"]).toBe(4.0);
    expect(corrupted["pipeline.mix_steps"]).toBe(4);
    expect(corrupted["pipeline.mix_alpha"]).toBe(1.0);
    expect(corrupted["pipeline.ref_img_index"]).toBe(10);
    expect(corrupted["pipeline.seed"]).toBe(42);
    expect(corrupted["pipeline.crf"]).toBe(16);

    // Out-of-range numeric values clamp to field min/max, while valid dynamic voice_preset IDs are preserved
    const clampedHigh = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.quality_preset": "balanced_hd",
        "pipeline.voice_preset": "qwen_vivian",
        "pipeline.num_inference_steps": 999,
        "pipeline.audio_guidance_scale": 12.5,
        "pipeline.text_guidance_scale": 0.1,
        "pipeline.mix_steps": 25,
        "pipeline.mix_alpha": 2.5,
        "pipeline.ref_img_index": 99,
        "pipeline.seed": -5,
        "pipeline.crf": 5,
      },
      "zh-CN",
    );
    expect(clampedHigh["pipeline.quality_preset"]).toBe("balanced_hd");
    expect(clampedHigh["pipeline.voice_preset"]).toBe("qwen_vivian");
    expect(clampedHigh["pipeline.num_inference_steps"]).toBe(60);
    expect(clampedHigh["pipeline.audio_guidance_scale"]).toBe(7.0);
    expect(clampedHigh["pipeline.text_guidance_scale"]).toBe(1.0);
    expect(clampedHigh["pipeline.mix_steps"]).toBe(8);
    expect(clampedHigh["pipeline.mix_alpha"]).toBe(1.0);
    expect(clampedHigh["pipeline.ref_img_index"]).toBe(30);
    expect(clampedHigh["pipeline.seed"]).toBe(0);
    expect(clampedHigh["pipeline.crf"]).toBe(12);

    const clampedLow = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.voice_preset": "x".repeat(65),
        "pipeline.num_inference_steps": 2,
        "pipeline.mix_steps": -3,
        "pipeline.mix_alpha": -0.2,
        "pipeline.ref_img_index": -1,
        "pipeline.crf": 40,
      },
      "zh-CN",
    );
    expect(clampedLow["pipeline.voice_preset"]).toBe("shen_zhiyi");
    expect(clampedLow["pipeline.num_inference_steps"]).toBe(8);
    expect(clampedLow["pipeline.mix_steps"]).toBe(0);
    expect(clampedLow["pipeline.mix_alpha"]).toBe(0.0);
    expect(clampedLow["pipeline.ref_img_index"]).toBe(0);
    expect(clampedLow["pipeline.crf"]).toBe(24);
  });

  it("detects and migrates partial legacy LongCat 1.5 drafts across every legacy indicator without false positives on partial 1.6 drafts", () => {
    const longCat16Fields: WorkflowField[] = [
      makeField({
        key: "pipeline.script_text",
        field_type: "textarea",
        default: "",
      }),
      makeField({
        key: "pipeline.quality_preset",
        field_type: "select",
        default: "cinema_flagship",
        options: ["cinema_flagship", "balanced_hd", "longform_fast"],
      }),
      makeField({
        key: "pipeline.upscale_mode",
        field_type: "select",
        default: "auto",
        options: ["auto", "seedvr2_7b_sharp", "seedvr2_7b", "flashvsr_plus", "none"],
      }),
      makeField({
        key: "pipeline.target_resolution",
        field_type: "select",
        default: "1920x1080",
        options: ["1920x1080", "1080x1920", "2560x1440", "1440x2560", "1280x720", "720x1280"],
      }),
    ];

    // Each individual legacy 1.5 indicator key (without a non-empty quality_preset) triggers 1280x720 upgrade to 1920x1080
    const legacyIndicators: Array<{ label: string; extra: Record<string, string | number | boolean> }> = [
      { label: "pipeline.render_quality", extra: { "pipeline.render_quality": "balanced_480p_ai2x" } },
      { label: "pipeline.acceleration_profile", extra: { "pipeline.acceleration_profile": "max" } },
      { label: "14.steps", extra: { "14.steps": 8 } },
      { label: "pipeline.max_segment_seconds", extra: { "pipeline.max_segment_seconds": 3.7 } },
      { label: "5.positive_prompt", extra: { "5.positive_prompt": "legacy studio prompt" } },
      { label: "15.seed", extra: { "15.seed": 123 } },
      { label: "pipeline.upscale_mode=ai_2x", extra: { "pipeline.upscale_mode": "ai_2x" } },
      { label: "pipeline.upscale_mode=lanczos", extra: { "pipeline.upscale_mode": "lanczos" } },
    ];

    for (const tc of legacyIndicators) {
      const migrated = mergeColabOneWorkflowDraftFields(
        longCat16Fields,
        {
          "pipeline.script_text": `legacy via ${tc.label}`,
          "pipeline.target_resolution": "1280x720",
          ...tc.extra,
        },
        "zh-CN",
      );
      expect(migrated["pipeline.target_resolution"], tc.label).toBe("1920x1080");
      expect(migrated["pipeline.upscale_mode"], tc.label).toBe("auto");
      expect(migrated["pipeline.script_text"], tc.label).toBe(`legacy via ${tc.label}`);
    }

    // Whitespace-only pipeline.quality_preset still treats a draft with legacy keys as legacy 1.5
    const whitespaceQualityPreset = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.quality_preset": "   ",
        "15.seed": 88,
        "pipeline.upscale_mode": "none",
        "pipeline.target_resolution": "1280x720",
      },
      "zh-CN",
    );
    expect(whitespaceQualityPreset["pipeline.quality_preset"]).toBe("cinema_flagship");
    expect(whitespaceQualityPreset["pipeline.upscale_mode"]).toBe("auto");
    expect(whitespaceQualityPreset["pipeline.target_resolution"]).toBe("1920x1080");

    // Partial 1.6 draft containing no legacy 1.5 keys must NOT mis-trigger migration
    const partial16Draft = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.script_text": "partial 1.6 without quality_preset",
        "pipeline.upscale_mode": "none",
        "pipeline.target_resolution": "1280x720",
      },
      "zh-CN",
    );
    expect(partial16Draft["pipeline.upscale_mode"]).toBe("none");
    expect(partial16Draft["pipeline.target_resolution"]).toBe("1280x720");
    expect(partial16Draft["pipeline.quality_preset"]).toBe("cinema_flagship");
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

  it("migrates LongCat 1.5 drafts forward to LongCat 1.6 native while preserving shared media/TTS fields, explicit non-720p resolution, and fresh 1.6 drafts", () => {
    const longCat16Fields: WorkflowField[] = [
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
      makeField({
        key: "pipeline.audio_source",
        field_type: "select",
        default: "tts",
        options: ["tts", "audio"],
      }),
      makeField({
        key: "pipeline.script_text",
        field_type: "textarea",
        default: "",
      }),
      makeField({
        key: "pipeline.tts_engine",
        field_type: "select",
        default: "fish_drama3",
        options: ["fish_drama3", "moss_tts_v1_5", "voxcpm2", "qwen3_tts"],
      }),
      makeField({
        key: "pipeline.voice_preset",
        field_type: "select",
        default: "shen_zhiyi",
        options: ["shen_zhiyi", "lin_xiaoman", "su_wanqing", "custom"],
      }),
      makeField({
        key: "pipeline.voice_ref_audio",
        field_type: "audio",
        default: "",
      }),
      makeField({
        key: "pipeline.pause_seconds",
        field_type: "number",
        default: 0.28,
      }),
      makeField({
        key: "pipeline.quality_preset",
        field_type: "select",
        default: "cinema_flagship",
        options: ["cinema_flagship", "balanced_hd", "longform_fast"],
      }),
      makeField({
        key: "pipeline.framing_style",
        field_type: "select",
        default: "half_body_gestures",
        options: ["half_body_gestures", "closeup_presenter", "full_body_dynamic"],
      }),
      makeField({
        key: "pipeline.upscale_mode",
        field_type: "select",
        default: "auto",
        options: ["auto", "seedvr2_7b_sharp", "seedvr2_7b", "flashvsr_plus", "none"],
      }),
      makeField({
        key: "pipeline.target_resolution",
        field_type: "select",
        default: "1920x1080",
        options: [
          "1920x1080",
          "1080x1920",
          "2560x1440",
          "1440x2560",
          "1280x720",
          "720x1280",
        ],
      }),
      makeField({
        key: "pipeline.sampling_mode",
        field_type: "select",
        default: "auto",
        options: ["auto", "non_distill_cfg", "distill_lora_mix"],
      }),
      makeField({
        key: "pipeline.num_inference_steps",
        field_type: "integer",
        default: 50,
      }),
      makeField({
        key: "pipeline.seed",
        field_type: "integer",
        default: 42,
      }),
      makeField({
        key: "pipeline.crf",
        field_type: "integer",
        default: 16,
      }),
      makeField({
        key: "pipeline.positive_prompt",
        field_type: "textarea",
        default: "",
      }),
      makeField({
        key: "pipeline.negative_prompt",
        field_type: "textarea",
        default: "",
      }),
    ];

    const validMedia: Record<string, ColabOneWorkflowDraftMediaMeta> = {
      "1.image": {
        id: "1".repeat(32),
        filename: "presenter_halfbody.png",
        kind: "image",
        mime: "image/png",
        size: 2048,
        sha256: "a".repeat(64),
        updated_at: 1700000100,
      },
      "pipeline.voice_ref_audio": {
        id: "2".repeat(32),
        filename: "host_clone.wav",
        kind: "audio",
        mime: "audio/wav",
        size: 4096,
        sha256: "b".repeat(64),
        updated_at: 1700000100,
      },
    };

    // 1. LongCat 1.5 draft (balanced_480p_ai2x + ai_2x + default 1280x720) migrates forward cleanly
    const migratedFrom15Ai2x = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "1.image": "stale-ghost.png",
        "pipeline.audio_source": "tts",
        "pipeline.script_text": "保留我的 1.5 口播文稿内容",
        "pipeline.tts_engine": "qwen3_tts",
        "pipeline.voice_preset": "qwen_vivian",
        "pipeline.pause_seconds": 0.4,
        "pipeline.render_quality": "balanced_480p_ai2x",
        "pipeline.acceleration_profile": "max",
        "pipeline.upscale_mode": "ai_2x",
        "pipeline.target_resolution": "1280x720",
        "pipeline.max_segment_seconds": 3.7,
        "14.steps": 8,
        "15.seed": 999,
      },
      "zh-CN",
      validMedia,
    );
    expect("pipeline.render_quality" in migratedFrom15Ai2x).toBe(false);
    expect("pipeline.acceleration_profile" in migratedFrom15Ai2x).toBe(false);
    expect("pipeline.max_segment_seconds" in migratedFrom15Ai2x).toBe(false);
    expect("14.steps" in migratedFrom15Ai2x).toBe(false);
    expect("15.seed" in migratedFrom15Ai2x).toBe(false);
    expect(migratedFrom15Ai2x["pipeline.quality_preset"]).toBe("cinema_flagship");
    expect(migratedFrom15Ai2x["pipeline.framing_style"]).toBe("half_body_gestures");
    expect(migratedFrom15Ai2x["pipeline.upscale_mode"]).toBe("auto");
    expect(migratedFrom15Ai2x["pipeline.target_resolution"]).toBe("1920x1080");
    expect(migratedFrom15Ai2x["pipeline.sampling_mode"]).toBe("auto");
    expect(migratedFrom15Ai2x["pipeline.num_inference_steps"]).toBe(50);
    expect(migratedFrom15Ai2x["pipeline.seed"]).toBe(42);
    expect(migratedFrom15Ai2x["pipeline.crf"]).toBe(16);
    expect(migratedFrom15Ai2x["1.image"]).toBe("presenter_halfbody.png");
    expect(migratedFrom15Ai2x["pipeline.voice_ref_audio"]).toBe("host_clone.wav");
    expect(migratedFrom15Ai2x["pipeline.script_text"]).toBe("保留我的 1.5 口播文稿内容");
    expect(migratedFrom15Ai2x["pipeline.tts_engine"]).toBe("qwen3_tts");
    expect(migratedFrom15Ai2x["pipeline.voice_preset"]).toBe("qwen_vivian");
    expect(migratedFrom15Ai2x["pipeline.pause_seconds"]).toBe(0.4);

    // 2. LongCat 1.5 native_720p draft with lanczos + explicit 1080x1920 resets lanczos to auto while preserving 1080x1920
    const migratedFrom15Lanczos = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.script_text": "竖屏口播保留 1080x1920",
        "pipeline.render_quality": "native_720p",
        "pipeline.acceleration_profile": "balanced",
        "pipeline.upscale_mode": "lanczos",
        "pipeline.target_resolution": "1080x1920",
      },
      "zh-CN",
    );
    expect("pipeline.render_quality" in migratedFrom15Lanczos).toBe(false);
    expect("pipeline.acceleration_profile" in migratedFrom15Lanczos).toBe(false);
    expect(migratedFrom15Lanczos["pipeline.upscale_mode"]).toBe("auto");
    expect(migratedFrom15Lanczos["pipeline.target_resolution"]).toBe("1080x1920");
    expect(migratedFrom15Lanczos["pipeline.script_text"]).toBe("竖屏口播保留 1080x1920");

    // 2b. LongCat 1.5 post-optimization draft with explicit upscale_mode "none" preserves "none"
    const migratedFrom15ExplicitNone = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.render_quality": "native_720p",
        "pipeline.upscale_mode": "none",
        "pipeline.target_resolution": "720x1280",
      },
      "zh-CN",
    );
    expect(migratedFrom15ExplicitNone["pipeline.upscale_mode"]).toBe("none");
    expect(migratedFrom15ExplicitNone["pipeline.target_resolution"]).toBe("720x1280");

    // 3. Pre-optimization LongCat draft (only legacy 14.steps/max_segment_seconds, no render_quality) migrating straight to 1.6 upgrades legacy "none" and "1280x720"
    const migratedFromPreOptTo16 = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.script_text": "极早期草稿迁移",
        "pipeline.upscale_mode": "none",
        "pipeline.target_resolution": "1280x720",
        "pipeline.max_segment_seconds": 9,
        "14.steps": 12,
      },
      "zh-CN",
    );
    expect(migratedFromPreOptTo16["pipeline.upscale_mode"]).toBe("auto");
    expect(migratedFromPreOptTo16["pipeline.target_resolution"]).toBe("1920x1080");
    expect(migratedFromPreOptTo16["pipeline.script_text"]).toBe("极早期草稿迁移");

    // 4. Fresh LongCat 1.6 draft (with pipeline.quality_preset) is preserved verbatim, including explicit 1280x720 and none
    const fresh16Draft = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.audio_source": "audio",
        "pipeline.quality_preset": "balanced_hd",
        "pipeline.framing_style": "closeup_presenter",
        "pipeline.upscale_mode": "flashvsr_plus",
        "pipeline.target_resolution": "1280x720",
        "pipeline.sampling_mode": "distill_lora_mix",
        "pipeline.num_inference_steps": 35,
        "pipeline.seed": 202606,
        "pipeline.crf": 14,
        "pipeline.positive_prompt": "Custom cinema rim light",
        "pipeline.negative_prompt": "extra fingers, blurry teeth",
      },
      "en-US",
      validMedia,
    );
    expect(fresh16Draft["pipeline.audio_source"]).toBe("audio");
    expect(fresh16Draft["pipeline.quality_preset"]).toBe("balanced_hd");
    expect(fresh16Draft["pipeline.framing_style"]).toBe("closeup_presenter");
    expect(fresh16Draft["pipeline.upscale_mode"]).toBe("flashvsr_plus");
    expect(fresh16Draft["pipeline.target_resolution"]).toBe("1280x720");
    expect(fresh16Draft["pipeline.sampling_mode"]).toBe("distill_lora_mix");
    expect(fresh16Draft["pipeline.num_inference_steps"]).toBe(35);
    expect(fresh16Draft["pipeline.seed"]).toBe(202606);
    expect(fresh16Draft["pipeline.crf"]).toBe(14);
    expect(fresh16Draft["pipeline.positive_prompt"]).toBe("Custom cinema rim light");
    expect(fresh16Draft["pipeline.negative_prompt"]).toBe("extra fingers, blurry teeth");
    expect(fresh16Draft["1.image"]).toBe("presenter_halfbody.png");

    // Fresh 1.6 draft with explicit upscale_mode "none" is also preserved verbatim
    const fresh16DraftNone = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.quality_preset": "longform_fast",
        "pipeline.upscale_mode": "none",
        "pipeline.target_resolution": "1280x720",
      },
      "en-US",
    );
    expect(fresh16DraftNone["pipeline.quality_preset"]).toBe("longform_fast");
    expect(fresh16DraftNone["pipeline.upscale_mode"]).toBe("none");
    expect(fresh16DraftNone["pipeline.target_resolution"]).toBe("1280x720");
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

  it("propagates real backend errors without mutating localStorage on text save", async () => {
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

    // Non-fallback backend rejection must NOT mutate localStorage
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).toBeNull();

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

  it("preserves fractional float updated_at timestamps across RPC and localStorage while sanitizing non-finite values", async () => {
    const storage = createMemoryStorage();
    const floatTs = 1_700_000_123.75;
    const mediaFloatTs = 1_700_000_456.25;

    const invoke = vi.fn(async (action: string, payload?: Record<string, unknown>) => {
      if (action === "comfyui.draft.save") {
        return {
          ok: true,
          draft: {
            workflow_id: payload?.workflow_id,
            fields: payload?.fields ?? {},
            media: {},
            updated_at: floatTs,
          },
        };
      }
      if (action === "comfyui.draft.get") {
        if (payload?.workflow_id === "wf-bad-ts") {
          return {
            draft: {
              workflow_id: "wf-bad-ts",
              fields: { "pipeline.script_text": "bad ts" },
              media: {},
              updated_at: "not-a-number",
            },
          };
        }
        return {
          draft: {
            workflow_id: payload?.workflow_id,
            fields: { "pipeline.script_text": "float ts" },
            media: {
              "1.image": {
                id: "a".repeat(32),
                filename: "host.png",
                kind: "image",
                mime: "image/png",
                size: 128,
                sha256: "b".repeat(64),
                updated_at: mediaFloatTs,
              },
            },
            updated_at: floatTs,
          },
        };
      }
      return { ok: true };
    });

    const client = createColabOneWorkflowDraftClient(invoke, storage);
    const saved = await client.saveColabOneWorkflowDraft("wf-float", {
      "pipeline.script_text": "float ts",
    });
    expect(saved?.updated_at).toBe(floatTs);

    const loaded = await client.loadColabOneWorkflowDraft("wf-float");
    expect(loaded?.updated_at).toBe(floatTs);
    expect(loaded?.media["1.image"]?.updated_at).toBe(mediaFloatTs);

    const badTsLoaded = await client.loadColabOneWorkflowDraft("wf-bad-ts");
    expect(badTsLoaded?.updated_at).toBe(0);

    // Offline localStorage mirror also preserves fractional updated_at values
    storage.setItem(
      COLABONE_STORAGE_KEYS.comfyDrafts,
      JSON.stringify({
        "wf-local-float": {
          workflow_id: "wf-local-float",
          fields: { "pipeline.script_text": "local float" },
          updated_at: 1_700_000_999.5,
        },
        "wf-local-bad": {
          workflow_id: "wf-local-bad",
          fields: { "pipeline.script_text": "local bad" },
          updated_at: null,
        },
      }),
    );
    const offlineClient = createColabOneWorkflowDraftClient(
      vi.fn(async () => {
        throw new Error("desktop bridge unavailable");
      }),
      storage,
      { allowLocalFallback: true },
    );
    const localFloat = await offlineClient.loadColabOneWorkflowDraft("wf-local-float");
    expect(localFloat?.updated_at).toBe(1_700_000_999.5);
    const localBad = await offlineClient.loadColabOneWorkflowDraft("wf-local-bad");
    expect(localBad?.updated_at).toBe(0);
  });

  it("drains chained writes queued while a previous write is in-flight before loadColabOneWorkflowDraft reads", async () => {
    const firstWriteGate = createDeferred<Record<string, unknown>>();
    const secondWriteGate = createDeferred<Record<string, unknown>>();
    const events: string[] = [];
    let saveIndex = 0;

    const invoke = vi.fn(async (action: string, payload?: Record<string, unknown>) => {
      if (action === "comfyui.draft.save") {
        saveIndex += 1;
        const idx = saveIndex;
        events.push(`save-${idx}:start`);
        const result =
          idx === 1 ? await firstWriteGate.promise : await secondWriteGate.promise;
        events.push(`save-${idx}:done`);
        return result;
      }
      if (action === "comfyui.draft.get") {
        events.push("get");
        return {
          draft: {
            workflow_id: payload?.workflow_id,
            fields: { "pipeline.script_text": "second write persisted" },
            media: {},
            updated_at: 1_700_000_200,
          },
        };
      }
      return { ok: true };
    });

    const client = createColabOneWorkflowDraftClient(invoke, createMemoryStorage());

    // Start write 1 (in-flight) and immediately chain write 2 onto the same workflow queue.
    const write1 = client.saveColabOneWorkflowDraft("wf-chain", {
      "pipeline.script_text": "first write",
    });
    const write2 = client.saveColabOneWorkflowDraft("wf-chain", {
      "pipeline.script_text": "second write persisted",
    });

    // Trigger load while write 1 is still pending and write 2 is chained behind it.
    const loadPromise = client.loadColabOneWorkflowDraft("wf-chain");
    await Promise.resolve();
    expect(events).toEqual(["save-1:start"]);

    // Resolve write 1; write 2 starts immediately and load must STILL wait for write 2.
    firstWriteGate.resolve({
      ok: true,
      draft: {
        workflow_id: "wf-chain",
        fields: { "pipeline.script_text": "first write" },
        media: {},
        updated_at: 1_700_000_100,
      },
    });
    await write1;
    await Promise.resolve();
    await Promise.resolve();
    expect(events).toEqual(["save-1:start", "save-1:done", "save-2:start"]);

    // Only after write 2 settles may comfyui.draft.get execute.
    secondWriteGate.resolve({
      ok: true,
      draft: {
        workflow_id: "wf-chain",
        fields: { "pipeline.script_text": "second write persisted" },
        media: {},
        updated_at: 1_700_000_200,
      },
    });
    await write2;
    const loaded = await loadPromise;
    expect(events).toEqual([
      "save-1:start",
      "save-1:done",
      "save-2:start",
      "save-2:done",
      "get",
    ]);
    expect(loaded?.fields["pipeline.script_text"]).toBe("second write persisted");
  });

  it("rejects __proto__, constructor, and prototype keys across localStorage reads, RPC normalization, and save sanitization", async () => {
    const storage = createMemoryStorage();
    // Inject raw JSON containing forbidden prototype-pollution keys at both map and field levels.
    storage.setItem(
      COLABONE_STORAGE_KEYS.comfyDrafts,
      '{"__proto__":{"workflow_id":"__proto__","fields":{"polluted":"yes"},"updated_at":10},' +
        '"constructor":{"workflow_id":"constructor","fields":{"polluted":"yes"},"updated_at":10},' +
        '"prototype":{"workflow_id":"prototype","fields":{"polluted":"yes"},"updated_at":10},' +
        '"wf-safe":{"workflow_id":"wf-safe","fields":{"__proto__":"bad","constructor":"bad","prototype":"bad","pipeline.script_text":"clean"},"updated_at":42}}',
    );

    const offlineClient = createColabOneWorkflowDraftClient(
      vi.fn(async () => {
        throw new Error("desktop bridge unavailable");
      }),
      storage,
      { allowLocalFallback: true },
    );

    expect(await offlineClient.loadColabOneWorkflowDraft("__proto__")).toBeNull();
    expect(await offlineClient.loadColabOneWorkflowDraft("constructor")).toBeNull();
    expect(await offlineClient.loadColabOneWorkflowDraft("prototype")).toBeNull();

    const safeLocal = await offlineClient.loadColabOneWorkflowDraft("wf-safe");
    expect(safeLocal).not.toBeNull();
    expect(Object.keys(safeLocal!.fields)).toEqual(["pipeline.script_text"]);
    expect(safeLocal!.fields["pipeline.script_text"]).toBe("clean");
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();

    // RPC normalization strips forbidden keys from fields and media maps.
    let capturedSavePayload: Record<string, unknown> | undefined;
    const rpcInvoke = vi.fn(async (action: string, payload?: Record<string, unknown>) => {
      if (action === "comfyui.draft.get") {
        return JSON.parse(
          '{"draft":{"workflow_id":"wf-rpc","updated_at":99,' +
            '"fields":{"__proto__":"bad","constructor":"bad","prototype":"bad","ok_key":"ok_val"},' +
            '"media":{"__proto__":{"id":"' +
            "a".repeat(32) +
            '","filename":"evil.png","kind":"image","mime":"image/png","size":10,"sha256":"' +
            "b".repeat(64) +
            '","updated_at":1},' +
            '"constructor":{"id":"' +
            "c".repeat(32) +
            '","filename":"evil2.png","kind":"image","mime":"image/png","size":10,"sha256":"' +
            "d".repeat(64) +
            '","updated_at":1},' +
            '"prototype":{"id":"' +
            "e".repeat(32) +
            '","filename":"evil3.png","kind":"image","mime":"image/png","size":10,"sha256":"' +
            "f".repeat(64) +
            '","updated_at":1},' +
            '"1.image":{"id":"' +
            "1".repeat(32) +
            '","filename":"host.png","kind":"image","mime":"image/png","size":64,"sha256":"' +
            "2".repeat(64) +
            '","updated_at":5}}}}',
        );
      }
      if (action === "comfyui.draft.save") {
        capturedSavePayload = payload;
        return {
          ok: true,
          draft: {
            workflow_id: payload?.workflow_id,
            fields: payload?.fields ?? {},
            media: {},
            updated_at: 100,
          },
        };
      }
      return { ok: true };
    });

    const rpcClient = createColabOneWorkflowDraftClient(rpcInvoke, createMemoryStorage());
    const normalized = await rpcClient.loadColabOneWorkflowDraft("wf-rpc");
    expect(normalized).not.toBeNull();
    expect(Object.keys(normalized!.fields)).toEqual(["ok_key"]);
    expect(Object.keys(normalized!.media)).toEqual(["1.image"]);

    // sanitizePrimitiveRecord inside saveColabOneWorkflowDraft strips forbidden own keys.
    const dirtyFields = JSON.parse(
      '{"__proto__":"evil","constructor":"evil","prototype":"evil","pipeline.script_text":"safe text"}',
    ) as Record<string, string | number | boolean>;
    await rpcClient.saveColabOneWorkflowDraft("wf-save", dirtyFields);
    const savedFields = (capturedSavePayload?.fields ?? {}) as Record<string, unknown>;
    expect(Object.keys(savedFields)).toEqual(["pipeline.script_text"]);
    expect(savedFields["pipeline.script_text"]).toBe("safe text");
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
  });

  it("canonicalizes case-insensitive CUSTOM voice preset and preserves explicit legacy 1.5 native_720p none 1280x720", () => {
    const longCat16Fields: WorkflowField[] = [
      makeField({
        key: "pipeline.voice_preset",
        field_type: "select",
        default: "shen_zhiyi",
        options: ["shen_zhiyi", "custom"],
      }),
      makeField({
        key: "pipeline.quality_preset",
        field_type: "select",
        default: "cinema_flagship",
        options: ["cinema_flagship", "balanced_hd", "longform_fast"],
      }),
      makeField({
        key: "pipeline.upscale_mode",
        field_type: "select",
        default: "auto",
        options: ["auto", "seedvr2_7b_sharp", "seedvr2_7b", "flashvsr_plus", "none"],
      }),
      makeField({
        key: "pipeline.target_resolution",
        field_type: "select",
        default: "1920x1080",
        options: [
          "1920x1080",
          "1080x1920",
          "2560x1440",
          "1440x2560",
          "1280x720",
          "720x1280",
        ],
      }),
    ];

    // Case-insensitive CUSTOM voice preset canonicalizes to lowercase "custom"
    const customMerged = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.voice_preset": " CUSTOM ",
      },
      "zh-CN",
    );
    expect(customMerged["pipeline.voice_preset"]).toBe("custom");

    // Legacy 1.5 draft explicitly set to native_720p + none + 1280x720 preserves both "none" and "1280x720"
    const legacy15Merged = mergeColabOneWorkflowDraftFields(
      longCat16Fields,
      {
        "pipeline.render_quality": "native_720p",
        "pipeline.upscale_mode": "none",
        "pipeline.target_resolution": "1280x720",
        "pipeline.voice_preset": "Custom",
      },
      "zh-CN",
    );
    expect(legacy15Merged["pipeline.upscale_mode"]).toBe("none");
    expect(legacy15Merged["pipeline.target_resolution"]).toBe("1280x720");
    expect(legacy15Merged["pipeline.voice_preset"]).toBe("custom");
  });
});

describe("createColabOneDraftStatusController", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("auto-hides saved status after visibleMs and keeps saving status active until settled", () => {
    vi.useFakeTimers();
    const history: ColabOneWorkflowDraftStatus[] = [];
    const controller = createColabOneDraftStatusController({
      onStatusChange: (status) => history.push(status),
    });

    controller.setStatus("saving");
    vi.advanceTimersByTime(COLABONE_DRAFT_STATUS_VISIBLE_MS * 3);
    expect(controller.getStatus()).toBe("saving");
    expect(history).toEqual(["saving"]);

    controller.setStatus("saved");
    expect(controller.getStatus()).toBe("saved");
    vi.advanceTimersByTime(COLABONE_DRAFT_STATUS_VISIBLE_MS - 1);
    expect(controller.getStatus()).toBe("saved");

    vi.advanceTimersByTime(1);
    expect(controller.getStatus()).toBe("idle");
    expect(history).toEqual(["saving", "saved", "idle"]);

    controller.dispose();
  });

  it("resets the auto-hide countdown when subsequent saves occur before expiry", () => {
    vi.useFakeTimers();
    let current: ColabOneWorkflowDraftStatus = "idle";
    const controller = createColabOneDraftStatusController({
      onStatusChange: (status) => {
        current = status;
      },
      visibleMs: 3000,
    });

    controller.setStatus("saved");
    vi.advanceTimersByTime(2000);
    expect(current).toBe("saved");

    controller.setStatus("saving");
    vi.advanceTimersByTime(2000);
    expect(current).toBe("saving");

    controller.setStatus("saved");
    vi.advanceTimersByTime(2999);
    expect(current).toBe("saved");
    vi.advanceTimersByTime(1);
    expect(current).toBe("idle");

    controller.dispose();
  });

  it("defers auto-hiding restored status set while hidden until drawer becomes visible", () => {
    vi.useFakeTimers();
    let visible = false;
    const history: ColabOneWorkflowDraftStatus[] = [];
    const controller = createColabOneDraftStatusController({
      onStatusChange: (status) => history.push(status),
      isVisible: () => visible,
      visibleMs: 3000,
    });

    controller.setStatus("restored");
    vi.advanceTimersByTime(10_000);
    expect(controller.getStatus()).toBe("restored");

    visible = true;
    controller.onVisibilityChange(true);
    vi.advanceTimersByTime(2999);
    expect(controller.getStatus()).toBe("restored");

    vi.advanceTimersByTime(1);
    expect(controller.getStatus()).toBe("idle");
    expect(history).toEqual(["restored", "idle"]);

    // Subsequent re-opens never revive an already dismissed restored indicator
    visible = false;
    controller.onVisibilityChange(false);
    visible = true;
    controller.onVisibilityChange(true);
    expect(controller.getStatus()).toBe("idle");
    expect(history).toEqual(["restored", "idle"]);

    controller.dispose();
  });

  it("auto-hides restored status immediately when already visible and honors clearIfSaving and dispose", () => {
    vi.useFakeTimers();
    const history: ColabOneWorkflowDraftStatus[] = [];
    const controller = createColabOneDraftStatusController({
      onStatusChange: (status) => history.push(status),
      isVisible: () => true,
      visibleMs: 2500,
    });

    controller.setStatus("restored");
    controller.clearIfSaving();
    expect(controller.getStatus()).toBe("restored");

    vi.advanceTimersByTime(2500);
    expect(controller.getStatus()).toBe("idle");

    controller.setStatus("saving");
    controller.clearIfSaving();
    expect(controller.getStatus()).toBe("idle");

    controller.setStatus("saved");
    controller.dispose();
    vi.advanceTimersByTime(5000);
    expect(history).toEqual(["restored", "idle", "saving", "idle", "saved"]);
  });

  it("throws non-fallback save errors without overwriting existing localStorage draft", async () => {
    const storage = createMemoryStorage();
    const wfId = "wf_reject_test";
    const existingRaw = JSON.stringify({
      [wfId]: {
        workflow_id: wfId,
        fields: { prompt: "original-safe" },
        updated_at: 100,
      },
    });
    storage.setItem(COLABONE_STORAGE_KEYS.comfyDrafts, existingRaw);

    const client = createColabOneWorkflowDraftClient(
      vi.fn(async () => {
        throw new Error("字段值无效：超出允许范围");
      }),
      storage,
    );

    await expect(
      client.saveColabOneWorkflowDraft(wfId, { prompt: "bad-value" }),
    ).rejects.toThrow("字段值无效");
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).toBe(existingRaw);
  });
});
