import { afterEach, describe, expect, it, vi } from "vitest";
import comfyCatalog from "../../catalog/comfyui.json";
import {
  CANVAS_FORMAT_ERROR,
  COLABONE_CANONICAL_VISUAL_PROMPT,
  COLABONE_DIGITAL_HUMAN_ENGINE_SPECIFIC_KEYS,
  COLABONE_DIGITAL_HUMAN_GROUP_KEY,
  COLABONE_LEGACY_VISUAL_PROMPTS,
  COLABONE_VOICE_PRESET_ID_RE,
  buildColabOneStudioWorkflowEntries,
  buildColabOneWorkflowOverrides,
  clampColabOneNumericFieldValue,
  defaultFieldValues,
  extractPublicHost,
  formatOutputSize,
  groupWorkflowFields,
  hasNonCommercialNotice,
  isColabOneBuiltinDigitalHumanWorkflow,
  isColabOnePresetVisualPrompt,
  isColabOneVoicePresetSupportedByEngine,
  isColabOneVoiceRefRequired,
  isColabOneWorkflowFieldVisible,
  isWorkflowFieldVisible,
  missingRequiredWorkflowFields,
  outputBlobURL,
  parseWorkflowImportJSON,
  partitionWorkflowFields,
  readColabOnePreferredDigitalHumanEngine,
  remapColabOnePresetPromptFieldsForLocale,
  resolveColabOneAudioSource,
  resolveColabOneDigitalHumanGpuContext,
  resolveColabOneFallbackVoicePreset,
  resolveColabOneInitialDigitalHumanWorkflowId,
  resolveColabOneLongCatNativeSamplingMode,
  resolveColabOneRecommendedDigitalHumanWorkflowId,
  resolveColabOneSelectedVoicePreset,
  resolveColabOneWorkflowFieldDefault,
  resolveColabOneWorkflowFieldPlaceholder,
  resolveSelectedTtsEngine,
  transferColabOneDigitalHumanFields,
  workflowModelsTotalBytes,
  writeColabOnePreferredDigitalHumanEngine,
} from "./comfyui";
import { COLABONE_LOCALE_LIST, t, type ColabOneLocale } from "./i18n";
import { COLABONE_STORAGE_KEYS } from "./storage";
import type {
  ColabOneComfyNodePatch,
  ColabOneComfyPipelineSpec,
  ColabOneComfyTtsEngineSpec,
  ColabOneComfyTtsVoicePreset,
  ComfyNodePatch,
  ComfyPipelineSpec,
  ComfyTtsEngineSpec,
  ComfyTtsVoicePreset,
  RuntimeStatus,
  TaskStatus,
  WorkflowField,
  WorkflowOutput,
  WorkflowTemplate,
} from "./types";

const mockCommand = vi.hoisted(() => vi.fn());

vi.mock("./api", () => ({
  colabOneCommand: mockCommand,
}));

describe("parseWorkflowImportJSON", () => {
  it("accepts a valid ComfyUI API workflow JSON", () => {
    const raw = JSON.stringify({
      "3": {
        class_type: "KSampler",
        inputs: {
          seed: 42,
          steps: 20,
          cfg: 7.5,
          model: ["4", 0],
        },
      },
      "6": {
        class_type: "CLIPTextEncode",
        inputs: {
          text: "a serene mountain lake at dawn",
          clip: ["4", 1],
        },
      },
    });
    const result = parseWorkflowImportJSON(raw);
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(Object.keys(result.workflow)).toEqual(["3", "6"]);
    }
  });

  it("rejects ComfyUI UI canvas format JSON with helpful Chinese guidance", () => {
    const canvasJson = JSON.stringify({
      last_node_id: 9,
      last_link_id: 9,
      nodes: [{ id: 3, type: "KSampler" }],
      links: [],
      version: 0.4,
    });
    const result = parseWorkflowImportJSON(canvasJson);
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.error).toBe(CANVAS_FORMAT_ERROR);
      expect(result.error).toContain("Dev Mode");
      expect(result.error).toContain("Save (API Format)");
    }
  });

  it("rejects empty strings and malformed JSON", () => {
    expect(parseWorkflowImportJSON("").ok).toBe(false);
    expect(parseWorkflowImportJSON("   \n ").ok).toBe(false);
    const bad = parseWorkflowImportJSON("{ not valid json");
    expect(bad.ok).toBe(false);
  });

  it("rejects non-object top-level JSON and empty objects", () => {
    expect(parseWorkflowImportJSON("[]").ok).toBe(false);
    expect(parseWorkflowImportJSON('"hello"').ok).toBe(false);
    expect(parseWorkflowImportJSON("123").ok).toBe(false);
    expect(parseWorkflowImportJSON("null").ok).toBe(false);
    expect(parseWorkflowImportJSON("{}").ok).toBe(false);
  });

  it("rejects nodes missing valid class_type or inputs", () => {
    const missingClass = parseWorkflowImportJSON(
      JSON.stringify({ "1": { inputs: { text: "hi" } } }),
    );
    expect(missingClass.ok).toBe(false);

    const blankClass = parseWorkflowImportJSON(
      JSON.stringify({ "1": { class_type: "  ", inputs: {} } }),
    );
    expect(blankClass.ok).toBe(false);

    const missingInputs = parseWorkflowImportJSON(
      JSON.stringify({ "1": { class_type: "KSampler" } }),
    );
    expect(missingInputs.ok).toBe(false);

    const arrayInputs = parseWorkflowImportJSON(
      JSON.stringify({ "1": { class_type: "KSampler", inputs: [] } }),
    );
    expect(arrayInputs.ok).toBe(false);

    const scalarNode = parseWorkflowImportJSON(JSON.stringify({ "1": "bad" }));
    expect(scalarNode.ok).toBe(false);
  });

  it("matches Go/Python canvas detection parity", () => {
    const linksOnlyCanvas = parseWorkflowImportJSON(
      JSON.stringify({ nodes: [], links: [] }),
    );
    expect(linksOnlyCanvas).toEqual({ ok: false, error: CANVAS_FORMAT_ERROR });

    const versionOnlyCanvas = parseWorkflowImportJSON(
      JSON.stringify({ nodes: [], version: 0.4 }),
    );
    expect(versionOnlyCanvas).toEqual({ ok: false, error: CANVAS_FORMAT_ERROR });

    // Array `nodes` without `links` or `version` is NOT canvas format — fails as invalid node
    const bareNodesArray = parseWorkflowImportJSON(JSON.stringify({ nodes: [] }));
    expect(bareNodesArray.ok).toBe(false);
    if (!bareNodesArray.ok) {
      expect(bareNodesArray.error).not.toBe(CANVAS_FORMAT_ERROR);
      expect(bareNodesArray.error).toBe(t("comfy.errInvalidNode", { id: "nodes" }));
    }

    // Valid API workflow whose node ID happens to be "nodes"
    const apiNodeNamedNodes = parseWorkflowImportJSON(
      JSON.stringify({
        nodes: { class_type: "KSampler", inputs: { steps: 20 } },
      }),
    );
    expect(apiNodeNamedNodes.ok).toBe(true);
  });

  it("enforces 2 MiB UTF-8 byte limit (including multi-byte UTF-8)", () => {
    const asciiTooLarge = JSON.stringify({
      "1": {
        class_type: "CLIPTextEncode",
        inputs: { text: "a".repeat(2 * 1024 * 1024) },
      },
    });
    expect(parseWorkflowImportJSON(asciiTooLarge)).toEqual({
      ok: false,
      error: t("comfy.errImportTooLarge"),
    });

    // ~700,000 CJK chars is < 2M JS code units, but > 2 MiB UTF-8 bytes (3 bytes each)
    const cjkPayload = "中".repeat(700_000);
    const cjkTooLarge = JSON.stringify({
      "1": {
        class_type: "CLIPTextEncode",
        inputs: { text: cjkPayload },
      },
    });
    expect(cjkTooLarge.length).toBeLessThan(2 * 1024 * 1024);
    expect(parseWorkflowImportJSON(cjkTooLarge)).toEqual({
      ok: false,
      error: t("comfy.errImportTooLarge"),
    });
  });

  it("enforces 500-node limit, valid node IDs, class_type length, and non-empty input keys", () => {
    const exact500: Record<string, unknown> = {};
    for (let i = 1; i <= 500; i += 1) {
      exact500[String(i)] = { class_type: "EmptyNode", inputs: {} };
    }
    expect(parseWorkflowImportJSON(JSON.stringify(exact500)).ok).toBe(true);

    exact500["501"] = { class_type: "EmptyNode", inputs: {} };
    expect(parseWorkflowImportJSON(JSON.stringify(exact500))).toEqual({
      ok: false,
      error: t("comfy.errImportTooManyNodes"),
    });

    // Valid extended node ID characters: letters, digits, _, :, -
    expect(
      parseWorkflowImportJSON(
        JSON.stringify({
          "sub:node_1-a": { class_type: "KSampler", inputs: { steps: 10 } },
        }),
      ).ok,
    ).toBe(true);

    // Invalid node IDs
    for (const badId of ["node.1", "bad id", "", "a".repeat(81)]) {
      const res = parseWorkflowImportJSON(
        JSON.stringify({
          [badId]: { class_type: "KSampler", inputs: {} },
        }),
      );
      expect(res).toEqual({
        ok: false,
        error: t("comfy.errImportInvalidNodeId", { id: badId }),
      });
    }

    // class_type trimmed length <= 160 allowed, > 160 rejected
    expect(
      parseWorkflowImportJSON(
        JSON.stringify({
          "1": { class_type: `  ${"X".repeat(160)}  `, inputs: {} },
        }),
      ).ok,
    ).toBe(true);
    expect(
      parseWorkflowImportJSON(
        JSON.stringify({
          "1": { class_type: "X".repeat(161), inputs: {} },
        }),
      ),
    ).toEqual({
      ok: false,
      error: t("comfy.errImportInvalidClassType", { id: "1" }),
    });

    // Empty and whitespace-only input keys rejected
    expect(
      parseWorkflowImportJSON(
        JSON.stringify({
          "1": { class_type: "KSampler", inputs: { "": 1 } },
        }),
      ),
    ).toEqual({
      ok: false,
      error: t("comfy.errImportInvalidInputKey", { id: "1" }),
    });
    expect(
      parseWorkflowImportJSON(
        JSON.stringify({
          "1": { class_type: "KSampler", inputs: { "   ": 1 } },
        }),
      ),
    ).toEqual({
      ok: false,
      error: t("comfy.errImportInvalidInputKey", { id: "1" }),
    });
  });
});

describe("defaultFieldValues", () => {
  it("builds initial form values from field defaults and sensible fallbacks", () => {
    const fields: WorkflowField[] = [
      {
        key: "prompt",
        label: "提示词",
        node_id: "6",
        input_name: "text",
        field_type: "textarea",
        default: "a cat in neon rain",
      },
      {
        key: "title",
        label: "标题",
        node_id: "7",
        input_name: "prefix",
        field_type: "text",
      },
      {
        key: "steps",
        label: "步数",
        node_id: "3",
        input_name: "steps",
        field_type: "integer",
        default: 0,
        min_value: 1,
      },
      {
        key: "cfg",
        label: "CFG",
        node_id: "3",
        input_name: "cfg",
        field_type: "number",
        default: "7.5",
      },
      {
        key: "denoise",
        label: "降噪强度",
        node_id: "3",
        input_name: "denoise",
        field_type: "number",
        min_value: 0.2,
      },
      {
        key: "enabled",
        label: "启用增强",
        node_id: "8",
        input_name: "enable",
        field_type: "boolean",
        default: true,
      },
      {
        key: "flag_default_false",
        label: "默认关闭",
        node_id: "8",
        input_name: "off",
        field_type: "boolean",
      },
      {
        key: "sampler",
        label: "采样器",
        node_id: "3",
        input_name: "sampler_name",
        field_type: "select",
        options: ["euler", "dpmpp_2m"],
      },
      {
        key: "ref_image",
        label: "参考图",
        node_id: "10",
        input_name: "image",
        field_type: "image",
        default: "portrait.png",
      },
      {
        key: "voice",
        label: "驱动音频",
        node_id: "11",
        input_name: "audio",
        field_type: "audio",
      },
    ];

    expect(defaultFieldValues(fields)).toEqual({
      prompt: "a cat in neon rain",
      title: "",
      steps: 1,
      cfg: 7.5,
      denoise: 0.2,
      enabled: true,
      flag_default_false: false,
      sampler: "euler",
      ref_image: "portrait.png",
      voice: "",
    });
  });

  it("returns an empty record for empty fields", () => {
    expect(defaultFieldValues([])).toEqual({});
  });

  it("parses string booleans case-insensitively, rounds integer defaults, and trims select defaults", () => {
    const fields: WorkflowField[] = [
      {
        key: "b.false_str",
        label: "False str",
        node_id: "b",
        input_name: "false_str",
        field_type: "boolean",
        default: "false",
      },
      {
        key: "b.false_upper",
        label: "False upper",
        node_id: "b",
        input_name: "false_upper",
        field_type: "boolean",
        default: "  FALSE ",
      },
      {
        key: "b.zero_str",
        label: "Zero str",
        node_id: "b",
        input_name: "zero_str",
        field_type: "boolean",
        default: "0",
      },
      {
        key: "b.true_str",
        label: "True str",
        node_id: "b",
        input_name: "true_str",
        field_type: "boolean",
        default: " True ",
      },
      {
        key: "b.one_str",
        label: "One str",
        node_id: "b",
        input_name: "one_str",
        field_type: "boolean",
        default: "1",
      },
      {
        key: "i.frac_num",
        label: "Fractional int",
        node_id: "i",
        input_name: "frac_num",
        field_type: "integer",
        default: 3.7,
      },
      {
        key: "i.frac_str",
        label: "Fractional int str",
        node_id: "i",
        input_name: "frac_str",
        field_type: "integer",
        default: " 3.2 ",
      },
      {
        key: "i.neg_zero",
        label: "Neg zero int",
        node_id: "i",
        input_name: "neg_zero",
        field_type: "integer",
        default: -0.1,
      },
      {
        key: "i.min_fallback",
        label: "Min fallback int",
        node_id: "i",
        input_name: "min_fallback",
        field_type: "integer",
        default: true,
        min_value: 1.6,
      },
      {
        key: "s.blank",
        label: "Blank select",
        node_id: "s",
        input_name: "blank",
        field_type: "select",
        default: "   ",
        options: ["voxcpm2", "qwen3_tts"],
      },
      {
        key: "s.padded",
        label: "Padded select",
        node_id: "s",
        input_name: "padded",
        field_type: "select",
        default: " qwen3_tts ",
        options: ["voxcpm2", "qwen3_tts"],
      },
    ];

    const values = defaultFieldValues(fields);
    expect(values).toEqual({
      "b.false_str": false,
      "b.false_upper": false,
      "b.zero_str": false,
      "b.true_str": true,
      "b.one_str": true,
      "i.frac_num": 4,
      "i.frac_str": 3,
      "i.neg_zero": 0,
      "i.min_fallback": 2,
      "s.blank": "voxcpm2",
      "s.padded": "qwen3_tts",
    });
    expect(Object.is(values["i.neg_zero"], 0)).toBe(true);
  });
});

describe("formatOutputSize", () => {
  it("formats bytes, kilobytes, and megabytes cleanly without boundary rollover glitches", () => {
    expect(formatOutputSize(0)).toBe("0 B");
    expect(formatOutputSize(-10)).toBe("0 B");
    expect(formatOutputSize(Number.NaN)).toBe("0 B");
    expect(formatOutputSize(512)).toBe("512 B");
    expect(formatOutputSize(1023)).toBe("1023 B");
    expect(formatOutputSize(1023.4)).toBe("1023 B");
    expect(formatOutputSize(1023.6)).toBe("1.0 KB");
    expect(formatOutputSize(1024)).toBe("1.0 KB");
    expect(formatOutputSize(1536)).toBe("1.5 KB");
    expect(formatOutputSize(1023.94 * 1024)).toBe("1023.9 KB");
    expect(formatOutputSize(1048575)).toBe("1.00 MB");
    expect(formatOutputSize(1024 * 1024)).toBe("1.00 MB");
    expect(formatOutputSize(2.5 * 1024 * 1024)).toBe("2.50 MB");
  });
});

describe("outputBlobURL", () => {
  afterEach(() => {
    mockCommand.mockReset();
    vi.restoreAllMocks();
  });

  async function sha256Hex(text: string): Promise<string> {
    const bytes = new TextEncoder().encode(text);
    const digest = await globalThis.crypto.subtle.digest("SHA-256", bytes);
    return Array.from(new Uint8Array(digest), (n) =>
      n.toString(16).padStart(2, "0"),
    ).join("");
  }

  it("downloads chunked asset via comfyui.output.asset and returns a blob URL", async () => {
    const chunk1 = btoa("hello ");
    const chunk2 = btoa("world");
    mockCommand
      .mockResolvedValueOnce({
        data: chunk1,
        offset: 0,
        size: 11,
        mime: "image/png",
      })
      .mockResolvedValueOnce({
        data: chunk2,
        offset: 6,
        size: 11,
        mime: "image/png",
      });

    const createSpy = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:mock-output-1");

    const url = await outputBlobURL("out_123");
    expect(url).toBe("blob:mock-output-1");
    expect(mockCommand).toHaveBeenNthCalledWith(1, "comfyui.output.asset", {
      id: "out_123",
      offset: 0,
    });
    expect(mockCommand).toHaveBeenNthCalledWith(2, "comfyui.output.asset", {
      id: "out_123",
      offset: 6,
    });
    expect(createSpy).toHaveBeenCalledTimes(1);
    const blob = createSpy.mock.calls[0][0] as Blob;
    expect(blob.size).toBe(11);
    expect(blob.type).toBe("image/png");
    expect(await blob.text()).toBe("hello world");
  });

  it("verifies SHA-256 checksums case-insensitively and rejects mismatches", async () => {
    const payload = "hello world";
    const expected = await sha256Hex(payload);

    mockCommand
      .mockResolvedValueOnce({
        data: btoa("hello "),
        offset: 0,
        size: 11,
        sha256: `  ${expected.toUpperCase()}  `,
        mime: "IMAGE/PNG ; charset=binary",
      })
      .mockResolvedValueOnce({
        data: btoa("world"),
        offset: 6,
        size: 11,
        sha256: expected,
        mime: "image/png",
      });

    const createSpy = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:sha-ok");

    const url = await outputBlobURL("out_sha_ok");
    expect(url).toBe("blob:sha-ok");
    expect(createSpy).toHaveBeenCalledTimes(1);

    mockCommand.mockResolvedValueOnce({
      data: btoa(payload),
      offset: 0,
      size: 11,
      sha256: "0".repeat(64),
      mime: "image/png",
    });
    await expect(outputBlobURL("out_sha_bad")).rejects.toThrow(
      t("comfy.errOutputChecksum"),
    );
  });

  it("respects AbortSignal before and between chunk fetches (2-arg and 3-arg forms)", async () => {
    const preAborted = new AbortController();
    preAborted.abort();
    await expect(outputBlobURL("out_abort_0", preAborted.signal)).rejects.toMatchObject({
      name: "AbortError",
    });
    expect(mockCommand).not.toHaveBeenCalled();

    const midAbort = new AbortController();
    const customFetch = vi.fn(async (_id: string, offset: number) => {
      if (offset === 0) {
        midAbort.abort();
        return { data: btoa("hello "), offset: 0, size: 11, mime: "video/mp4" };
      }
      return { data: btoa("world"), offset: 6, size: 11, mime: "video/mp4" };
    });
    const createSpy = vi.spyOn(URL, "createObjectURL");

    await expect(
      outputBlobURL({ id: "out_abort_mid" }, customFetch, midAbort.signal),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(customFetch).toHaveBeenCalledTimes(1);
    expect(createSpy).not.toHaveBeenCalled();
  });

  it("enforces strict MIME allowlist and blocks SVG, HTML, PDF, octet-stream, and empty MIME", async () => {
    const createSpy = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:allowed");
    for (const allowedMime of [
      "image/png",
      "image/jpeg",
      "image/webp",
      "image/gif",
      "video/mp4",
      "video/webm",
      "audio/wav",
      "audio/mpeg",
      "audio/flac",
      "audio/wav; codecs=1",
    ]) {
      mockCommand.mockResolvedValueOnce({
        data: btoa("abc"),
        offset: 0,
        size: 3,
        mime: allowedMime,
      });
      await expect(outputBlobURL("out_ok")).resolves.toBe("blob:allowed");
    }
    expect(createSpy).toHaveBeenCalledTimes(10);

    for (const disallowedMime of [
      "image/svg+xml",
      "text/html",
      "text/plain",
      "application/pdf",
      "application/octet-stream",
      "",
      undefined,
    ]) {
      mockCommand.mockResolvedValueOnce({
        data: btoa("<svg></svg>"),
        offset: 0,
        size: 11,
        mime: disallowedMime,
      });
      await expect(outputBlobURL("out_blocked")).rejects.toThrow(
        t("comfy.errUnsupportedPreview"),
      );
    }
  });

  it("rejects invalid base64, unsafe sizes, and missing/mismatched chunk offsets", async () => {
    mockCommand.mockResolvedValueOnce({
      data: "!!!not-valid-base64!!!",
      offset: 0,
      size: 3,
      mime: "image/png",
    });
    await expect(outputBlobURL("out_bad_b64")).rejects.toThrow(
      t("comfy.errInvalidOutputTransfer"),
    );

    for (const badSize of [0, -5, 11.5, Number.NaN, 512 * 1024 ** 2 + 1, "11", true]) {
      mockCommand.mockResolvedValueOnce({
        data: btoa("abc"),
        offset: 0,
        size: badSize,
        mime: "image/png",
      });
      await expect(outputBlobURL("out_bad_size")).rejects.toThrow(
        t("comfy.errInvalidOutputSize"),
      );
    }

    // Mismatched offset on chunk 0
    mockCommand.mockResolvedValueOnce({
      data: btoa("abc"),
      offset: 99,
      size: 3,
      mime: "image/png",
    });
    await expect(outputBlobURL("out_bad_off0")).rejects.toThrow(
      t("comfy.errIncompleteTransfer"),
    );

    // Missing offset on chunk 2 (offset > 0 requires strict part.offset === offset)
    mockCommand
      .mockResolvedValueOnce({
        data: btoa("hello "),
        offset: 0,
        size: 11,
        mime: "image/png",
      })
      .mockResolvedValueOnce({
        data: btoa("world"),
        size: 11,
        mime: "image/png",
      });
    await expect(outputBlobURL("out_missing_off2")).rejects.toThrow(
      t("comfy.errIncompleteTransfer"),
    );
  });

  it("fails closed with comfy.errOutputChecksum on malformed non-64-hex part.sha256", async () => {
    const createSpy = vi.spyOn(URL, "createObjectURL");

    for (const badSha of [
      "",
      "   ",
      "not-a-valid-sha256",
      "a".repeat(63),
      "a".repeat(65),
      "g".repeat(64),
      null,
      12345,
      true,
      {},
    ]) {
      mockCommand.mockResolvedValueOnce({
        data: btoa("hello"),
        offset: 0,
        size: 5,
        sha256: badSha,
        mime: "image/png",
      });
      await expect(outputBlobURL("out_malformed_sha")).rejects.toThrow(
        t("comfy.errOutputChecksum"),
      );
    }

    // Malformed sha256 on a subsequent chunk also fails closed
    const validSha = await sha256Hex("hello world");
    mockCommand
      .mockResolvedValueOnce({
        data: btoa("hello "),
        offset: 0,
        size: 11,
        sha256: validSha,
        mime: "image/png",
      })
      .mockResolvedValueOnce({
        data: btoa("world"),
        offset: 6,
        size: 11,
        sha256: "xyz-malformed",
        mime: "image/png",
      });
    await expect(outputBlobURL("out_malformed_sha_chunk2")).rejects.toThrow(
      t("comfy.errOutputChecksum"),
    );

    expect(createSpy).not.toHaveBeenCalled();
  });

  it("loads Drive-backed chunks cleanly when sha256 is omitted", async () => {
    const createSpy = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:drive-no-sha");
    mockCommand
      .mockResolvedValueOnce({
        data: btoa("hello "),
        offset: 0,
        size: 11,
        mime: "image/png",
        source: "drive",
      })
      .mockResolvedValueOnce({
        data: btoa("world"),
        offset: 6,
        size: 11,
        mime: "image/png",
        source: "drive",
      });

    const url = await outputBlobURL("drive_no_sha");
    expect(url).toBe("blob:drive-no-sha");
    expect(createSpy).toHaveBeenCalledTimes(1);
    const blob = createSpy.mock.calls[0]?.[0] as Blob;
    expect(blob.type).toBe("image/png");
    expect(blob.size).toBe(11);
  });
});

describe("extractPublicHost", () => {
  it("extracts clean host-only values for valid URLs and fails closed to empty string on unsafe or malformed inputs", () => {
    expect(
      extractPublicHost("https://comfy.example.com/_colabone_auth?token=secret"),
    ).toBe("comfy.example.com");
    expect(
      extractPublicHost("https://comfy.example.com/_colabone_auth?token=secret123"),
    ).toBe("comfy.example.com");
    expect(
      extractPublicHost("comfy.example.com/_colabone_auth?token=secret123"),
    ).toBe("comfy.example.com");
    expect(
      extractPublicHost("comfy.example.com:8188/path?token=x"),
    ).toBe("comfy.example.com:8188");
    expect(
      extractPublicHost("comfy.example.com:8188/_colabone_auth?token=secret123"),
    ).toBe("comfy.example.com:8188");
    expect(
      extractPublicHost("//comfy.example.com/_colabone_auth?token=secret123#frag"),
    ).toBe("comfy.example.com");
    expect(
      extractPublicHost("//user:pass@comfy.example.com/a?token=x"),
    ).toBe("comfy.example.com");
    expect(
      extractPublicHost("https://user:supersecret@comfy.example.com/a?token=secret123"),
    ).toBe("comfy.example.com");
    expect(extractPublicHost("[::1]:8188?token=x")).toBe("[::1]:8188");

    // Strict fail-closed validation: schemeless userinfo, malformed hosts/ports,
    // non-HTTP(S) schemes, and semicolon/token smuggling must return "".
    expect(
      extractPublicHost("user:supersecret@comfy.example.com/a?token=secret123"),
    ).toBe("");
    expect(
      extractPublicHost(
        "https://user:supersecret@bad host:abc/path?token=secret123#frag",
      ),
    ).toBe("");
    expect(extractPublicHost("not a url?token=secret123")).toBe("");
    expect(extractPublicHost("javascript:token=SECRET123")).toBe("");
    expect(extractPublicHost("javascript:alert(1)?token=SECRET123")).toBe("");
    expect(extractPublicHost("host.example.com;token=SECRET123")).toBe("");
    expect(
      extractPublicHost("https://host.example.com:badport;token=SECRET123"),
    ).toBe("");
    expect(extractPublicHost("?token=secret123")).toBe("");
    expect(extractPublicHost("")).toBe("");
    expect(extractPublicHost("   ")).toBe("");
  });
});

function makeField(overrides: Partial<WorkflowField> & { key: string }): WorkflowField {
  const [nodeId, inputName] = overrides.key.includes(".")
    ? overrides.key.split(".", 2)
    : ["1", overrides.key];
  return {
    label: overrides.key,
    node_id: nodeId,
    input_name: inputName,
    field_type: "text",
    ...overrides,
  };
}

function makeTtsEngine(
  overrides: Partial<ComfyTtsEngineSpec> & { id: string },
): ComfyTtsEngineSpec {
  return {
    name: overrides.id,
    description: "",
    repo: `org/${overrides.id}`,
    revision: "a".repeat(40),
    license: "Apache-2.0",
    commercial_ok: true,
    license_notice: "",
    min_vram_mib: 8192,
    sample_rate: 48000,
    supports_voice_clone: true,
    supports_voice_design: false,
    supports_emotion_ref: false,
    supports_inline_tags: false,
    ...overrides,
  };
}

function makeWorkflow(
  overrides: Partial<WorkflowTemplate> = {},
): WorkflowTemplate {
  return {
    id: "digital_human_infinitetalk",
    name: "InfiniteTalk Digital Human",
    description: "Long-video digital human pipeline",
    source: "builtin",
    min_vram_mib: 24576,
    node_count: 11,
    fields: [],
    commercial_ok: true,
    license: "Apache-2.0",
    license_notice: "",
    pipeline: {
      kind: "digital_human",
      video_backend: "infinitetalk",
      default_tts_engine: "voxcpm2",
      supported_tts_engines: ["voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"],
    },
    ...overrides,
  };
}

describe("isWorkflowFieldVisible", () => {
  it("returns true when visible_when is undefined, null, or empty", () => {
    const fUndefined = makeField({ key: "1.image" });
    const fNull = makeField({ key: "1.image", visible_when: null });
    const fEmpty = makeField({ key: "1.image", visible_when: {} });

    expect(isWorkflowFieldVisible(fUndefined, {})).toBe(true);
    expect(isWorkflowFieldVisible(fNull, {})).toBe(true);
    expect(isWorkflowFieldVisible(fEmpty, {})).toBe(true);
  });

  it("matches single string and boolean visible_when conditions strictly", () => {
    const ttsField = makeField({
      key: "pipeline.script_text",
      visible_when: { "pipeline.audio_source": "tts" },
    });
    expect(
      isWorkflowFieldVisible(ttsField, { "pipeline.audio_source": "tts" }),
    ).toBe(true);
    expect(
      isWorkflowFieldVisible(ttsField, { "pipeline.audio_source": "audio" }),
    ).toBe(false);
    // Missing pipeline.audio_source defaults to "tts" (matching resolveColabOneAudioSource).
    expect(isWorkflowFieldVisible(ttsField, {})).toBe(true);

    const audioField = makeField({
      key: "2.audio",
      visible_when: { "pipeline.audio_source": "audio" },
    });
    expect(isWorkflowFieldVisible(audioField, {})).toBe(false);

    const nonAudioStringField = makeField({
      key: "pipeline.custom_detail",
      visible_when: { "pipeline.mode": "pro" },
    });
    expect(isWorkflowFieldVisible(nonAudioStringField, {})).toBe(false);

    const boolField = makeField({
      key: "5.detail",
      visible_when: { "5.enable_hd": true },
    });
    expect(isWorkflowFieldVisible(boolField, { "5.enable_hd": true })).toBe(true);
    expect(isWorkflowFieldVisible(boolField, { "5.enable_hd": false })).toBe(false);
    expect(isWorkflowFieldVisible(boolField, { "5.enable_hd": "true" })).toBe(
      false,
    );
  });

  it("evaluates multi-key visible_when as a strict logical AND", () => {
    const emotionRefField = makeField({
      key: "pipeline.emotion_ref_audio",
      field_type: "audio",
      visible_when: {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "indextts2",
      },
    });

    expect(
      isWorkflowFieldVisible(emotionRefField, {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "indextts2",
      }),
    ).toBe(true);

    expect(
      isWorkflowFieldVisible(emotionRefField, {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "voxcpm2",
      }),
    ).toBe(false);

    expect(
      isWorkflowFieldVisible(emotionRefField, {
        "pipeline.audio_source": "audio",
        "pipeline.tts_engine": "indextts2",
      }),
    ).toBe(false);

    expect(
      isWorkflowFieldVisible(emotionRefField, {
        "pipeline.audio_source": "audio",
        "pipeline.tts_engine": "fish_s2_pro",
      }),
    ).toBe(false);
  });

  it("trims string comparisons and ignores Object.prototype keys", () => {
    const ttsField = makeField({
      key: "pipeline.script_text",
      visible_when: { "pipeline.audio_source": "tts" },
    });
    expect(
      isWorkflowFieldVisible(ttsField, { "pipeline.audio_source": " tts " }),
    ).toBe(true);

    const protoField = makeField({
      key: "9.safe",
      visible_when: { toString: "anything" },
    });
    expect(isWorkflowFieldVisible(protoField, {})).toBe(false);
  });
});

describe("clampColabOneNumericFieldValue", () => {
  it("rounds integers, applies ceil/floor bounds, clamps floats, normalizes -0, and handles non-finite or inverted bounds", () => {
    const intField = makeField({
      key: "n.int",
      field_type: "integer",
      min_value: 1.2,
      max_value: 9.8,
    });
    expect(clampColabOneNumericFieldValue(0.4, intField)).toBe(2);
    expect(clampColabOneNumericFieldValue(5.6, intField)).toBe(6);
    expect(clampColabOneNumericFieldValue(12.9, intField)).toBe(9);

    const floatField = makeField({
      key: "n.float",
      field_type: "number",
      min_value: 0.5,
      max_value: 7.0,
    });
    expect(clampColabOneNumericFieldValue(0.1, floatField)).toBe(0.5);
    expect(clampColabOneNumericFieldValue(3.25, floatField)).toBe(3.25);
    expect(clampColabOneNumericFieldValue(12.4, floatField)).toBe(7.0);

    const zeroMinInt = makeField({
      key: "n.zero_int",
      field_type: "integer",
      min_value: 0,
    });
    expect(Object.is(clampColabOneNumericFieldValue(-0.1, zeroMinInt), 0)).toBe(
      true,
    );
    expect(Object.is(clampColabOneNumericFieldValue(-0, makeField({ key: "n.unbounded", field_type: "number" })), 0)).toBe(
      true,
    );

    // Non-finite numbers pass through unchanged
    expect(Number.isNaN(clampColabOneNumericFieldValue(Number.NaN, intField))).toBe(true);
    expect(clampColabOneNumericFieldValue(Number.POSITIVE_INFINITY, floatField)).toBe(
      Number.POSITIVE_INFINITY,
    );

    // Inverted integer bounds (ceil(5.8)=6 > floor(5.2)=5) return the rounded value without corrupting it
    const invertedInt = makeField({
      key: "n.inverted",
      field_type: "integer",
      min_value: 5.8,
      max_value: 5.2,
    });
    expect(clampColabOneNumericFieldValue(4.7, invertedInt)).toBe(5);
  });
});

describe("resolveColabOneAudioSource", () => {
  it("defaults missing, blank, or prototype-inherited values to 'tts' and trims/lowercases explicit sources", () => {
    expect(resolveColabOneAudioSource(undefined)).toBe("tts");
    expect(resolveColabOneAudioSource(null)).toBe("tts");
    expect(resolveColabOneAudioSource({})).toBe("tts");
    expect(resolveColabOneAudioSource({ "pipeline.audio_source": "" })).toBe("tts");
    expect(resolveColabOneAudioSource({ "pipeline.audio_source": "   " })).toBe("tts");
    expect(
      resolveColabOneAudioSource(
        Object.create({ "pipeline.audio_source": "audio" }) as Record<
          string,
          string | number | boolean
        >,
      ),
    ).toBe("tts");
    expect(resolveColabOneAudioSource({ "pipeline.audio_source": " TTS " })).toBe("tts");
    expect(resolveColabOneAudioSource({ "pipeline.audio_source": " Audio " })).toBe("audio");
    expect(resolveColabOneAudioSource({ "pipeline.audio_source": "AUDIO" })).toBe("audio");
  });
});

describe("isColabOneWorkflowFieldVisible", () => {
  const nativeWf = makeWorkflow({
    id: "digital-human-longcat",
    pipeline: {
      kind: "digital_human",
      video_backend: "longcat_avatar_native",
    },
  });
  const cfgField = makeField({ key: "pipeline.audio_guidance_scale", field_type: "number" });
  const textCfgField = makeField({ key: "pipeline.text_guidance_scale", field_type: "number" });
  const stepsField = makeField({ key: "pipeline.num_inference_steps", field_type: "integer" });
  const mixStepsField = makeField({ key: "pipeline.mix_steps", field_type: "integer" });
  const mixAlphaField = makeField({ key: "pipeline.mix_alpha", field_type: "number" });
  const ttsOnlyField = makeField({
    key: "pipeline.script_text",
    field_type: "textarea",
    visible_when: { "pipeline.audio_source": "tts" },
  });

  it("enforces visible_when rules and hides mismatched LongCat 1.6 sampling-mode controls", () => {
    // visible_when always checked first
    expect(
      isColabOneWorkflowFieldVisible(nativeWf, ttsOnlyField, {
        "pipeline.audio_source": "audio",
      }),
    ).toBe(false);
    expect(
      isColabOneWorkflowFieldVisible(nativeWf, ttsOnlyField, {
        "pipeline.audio_source": "tts",
      }),
    ).toBe(true);

    // non_distill_cfg (cinema_flagship / auto / unknown / whitespace quality_preset) shows CFG+steps and hides mix_*
    for (const presetValues of [
      { "pipeline.quality_preset": "cinema_flagship", "pipeline.sampling_mode": "auto" },
      { "pipeline.quality_preset": "   ", "pipeline.sampling_mode": "auto" },
      { "pipeline.quality_preset": "unknown_preset", "pipeline.sampling_mode": "auto" },
      { "pipeline.quality_preset": "balanced_hd", "pipeline.sampling_mode": "non_distill_cfg" },
    ]) {
      expect(isColabOneWorkflowFieldVisible(nativeWf, cfgField, presetValues)).toBe(true);
      expect(isColabOneWorkflowFieldVisible(nativeWf, textCfgField, presetValues)).toBe(true);
      expect(isColabOneWorkflowFieldVisible(nativeWf, stepsField, presetValues)).toBe(true);
      expect(isColabOneWorkflowFieldVisible(nativeWf, mixStepsField, presetValues)).toBe(false);
      expect(isColabOneWorkflowFieldVisible(nativeWf, mixAlphaField, presetValues)).toBe(false);
    }

    // distill_lora_mix (balanced_hd / longform_fast / explicit distill_lora_mix) shows mix_* and hides CFG+steps
    for (const presetValues of [
      { "pipeline.quality_preset": "balanced_hd", "pipeline.sampling_mode": "auto" },
      { "pipeline.quality_preset": "longform_fast", "pipeline.sampling_mode": "auto" },
      { "pipeline.quality_preset": "cinema_flagship", "pipeline.sampling_mode": "distill_lora_mix" },
    ]) {
      expect(isColabOneWorkflowFieldVisible(nativeWf, cfgField, presetValues)).toBe(false);
      expect(isColabOneWorkflowFieldVisible(nativeWf, textCfgField, presetValues)).toBe(false);
      expect(isColabOneWorkflowFieldVisible(nativeWf, stepsField, presetValues)).toBe(false);
      expect(isColabOneWorkflowFieldVisible(nativeWf, mixStepsField, presetValues)).toBe(true);
      expect(isColabOneWorkflowFieldVisible(nativeWf, mixAlphaField, presetValues)).toBe(true);
    }

    // Non-native workflows ignore LongCat 1.6 sampling-mode gating
    const legacyWf = makeWorkflow({
      id: "digital-human-infinitetalk",
      pipeline: { kind: "digital_human", video_backend: "infinitetalk" },
    });
    expect(isColabOneWorkflowFieldVisible(legacyWf, mixStepsField, {})).toBe(true);
    expect(isColabOneWorkflowFieldVisible(undefined, mixStepsField, {})).toBe(true);
  });
});

describe("groupWorkflowFields", () => {
  it("returns empty array for empty input", () => {
    expect(groupWorkflowFields([])).toEqual([]);
  });

  it("falls back to a single ungrouped bucket when no field has a non-empty group", () => {
    const fields = [
      makeField({ key: "6.text" }),
      makeField({ key: "3.steps", group: "" }),
      makeField({ key: "3.seed", group: "   " }),
    ];
    expect(groupWorkflowFields(fields)).toEqual([
      { group: "", fields },
    ]);
  });

  it("orders canonical groups as media -> voice -> video -> advanced and preserves intra-group order", () => {
    const fAdv1 = makeField({ key: "8.steps", group: "advanced" });
    const fVideo1 = makeField({ key: "5.positive_prompt", group: "video" });
    const fMedia1 = makeField({ key: "1.image", group: "media" });
    const fVoice1 = makeField({ key: "pipeline.audio_source", group: "voice" });
    const fVoice2 = makeField({ key: "pipeline.tts_engine", group: "voice" });
    const fMedia2 = makeField({ key: "1.mask", group: " media " });
    const fAdv2 = makeField({ key: "8.seed", group: "advanced" });

    const grouped = groupWorkflowFields([
      fAdv1,
      fVideo1,
      fMedia1,
      fVoice1,
      fVoice2,
      fMedia2,
      fAdv2,
    ]);

    expect(grouped.map((g) => g.group)).toEqual([
      "media",
      "voice",
      "video",
      "advanced",
    ]);
    expect(grouped[0].fields).toEqual([fMedia1, fMedia2]);
    expect(grouped[1].fields).toEqual([fVoice1, fVoice2]);
    expect(grouped[2].fields).toEqual([fVideo1]);
    expect(grouped[3].fields).toEqual([fAdv1, fAdv2]);
  });

  it("omits empty canonical groups and places custom groups before trailing ungrouped fields", () => {
    const fUngrouped = makeField({ key: "9.note" });
    const fCustomB = makeField({ key: "7.b", group: "lighting" });
    const fVideo = makeField({ key: "5.prompt", group: "video" });
    const fCustomA = makeField({ key: "7.a", group: "camera" });
    const fMedia = makeField({ key: "1.image", group: "media" });

    const grouped = groupWorkflowFields([
      fUngrouped,
      fCustomB,
      fVideo,
      fCustomA,
      fMedia,
    ]);

    expect(grouped.map((g) => g.group)).toEqual([
      "media",
      "video",
      "lighting",
      "camera",
      "",
    ]);
    expect(grouped[4].fields).toEqual([fUngrouped]);
  });

  it("places basic first in canonical ordering (basic -> media -> voice -> video -> advanced -> custom)", () => {
    const fCustom = makeField({ key: "9.extra", group: "custom" });
    const fAdv = makeField({ key: "15.seed", group: "advanced" });
    const fVideo = makeField({ key: "5.positive_prompt", group: "video" });
    const fVoice = makeField({ key: "pipeline.tts_engine", group: "voice" });
    const fMedia = makeField({ key: "1.mask", group: "media" });
    const fBasic1 = makeField({ key: "1.image", group: "basic" });
    const fBasic2 = makeField({ key: "pipeline.script_text", group: " basic " });

    const grouped = groupWorkflowFields([
      fCustom,
      fAdv,
      fVideo,
      fVoice,
      fMedia,
      fBasic1,
      fBasic2,
    ]);

    expect(grouped.map((g) => g.group)).toEqual([
      "basic",
      "media",
      "voice",
      "video",
      "advanced",
      "custom",
    ]);
    expect(grouped[0].fields).toEqual([fBasic1, fBasic2]);
  });
});

describe("partitionWorkflowFields", () => {
  it("returns empty primary and advanced arrays for empty input", () => {
    expect(partitionWorkflowFields([])).toEqual({ primary: [], advanced: [] });
  });

  it("splits mixed basic and advanced fields while preserving exact order", () => {
    const fImg = makeField({ key: "1.image", group: "basic" });
    const fTts = makeField({ key: "pipeline.tts_engine", group: "advanced" });
    const fScript = makeField({ key: "pipeline.script_text", group: "basic" });
    const fMedia = makeField({ key: "1.mask", group: "media" });
    const fSteps = makeField({ key: "15.steps", group: "advanced" });
    const fRes = makeField({ key: "pipeline.target_resolution", group: "basic" });
    const fSeed = makeField({ key: "15.seed", group: "advanced" });

    const result = partitionWorkflowFields([
      fImg,
      fTts,
      fScript,
      fMedia,
      fSteps,
      fRes,
      fSeed,
    ]);

    expect(result.primary).toEqual([fImg, fScript, fMedia, fRes]);
    expect(result.advanced).toEqual([fTts, fSteps, fSeed]);
  });

  it("places all ungrouped and custom-grouped fields in primary", () => {
    const f1 = makeField({ key: "6.text" });
    const f2 = makeField({ key: "3.steps", group: "" });
    const f3 = makeField({ key: "3.cfg", group: "   " });
    const f4 = makeField({ key: "7.lighting", group: "lighting" });

    const result = partitionWorkflowFields([f1, f2, f3, f4]);

    expect(result).toEqual({
      primary: [f1, f2, f3, f4],
      advanced: [],
    });
  });

  it("falls back to returning all fields in primary when every field is advanced", () => {
    const f1 = makeField({ key: "15.steps", group: "advanced" });
    const f2 = makeField({ key: "15.seed", group: " advanced " });
    const input = [f1, f2];

    const result = partitionWorkflowFields(input);

    expect(result).toEqual({
      primary: [f1, f2],
      advanced: [],
    });
    expect(result.primary).not.toBe(input);
  });

  it("trims whitespace and normalizes casing around group names", () => {
    const fBasic = makeField({ key: "1.image", group: "Basic" });
    const fAdvPadded = makeField({ key: "15.steps", group: " advanced " });
    const fAdvUpper = makeField({ key: "15.cfg", group: "ADVANCED" });
    const fAdvTabbed = makeField({ key: "15.seed", group: "\tAdvanced\n" });

    const result = partitionWorkflowFields([
      fBasic,
      fAdvPadded,
      fAdvUpper,
      fAdvTabbed,
    ]);

    expect(result).toEqual({
      primary: [fBasic],
      advanced: [fAdvPadded, fAdvUpper, fAdvTabbed],
    });

    const grouped = groupWorkflowFields([fAdvUpper, fBasic, fAdvPadded]);
    expect(grouped.map((g) => g.group)).toEqual(["basic", "advanced"]);
    expect(grouped[0].fields).toEqual([fBasic]);
    expect(grouped[1].fields).toEqual([fAdvUpper, fAdvPadded]);
  });
});

describe("resolveSelectedTtsEngine", () => {
  const engines: ComfyTtsEngineSpec[] = [
    makeTtsEngine({ id: "voxcpm2", name: "VoxCPM2" }),
    makeTtsEngine({ id: "qwen3_tts", name: "Qwen3-TTS 1.7B", sample_rate: 24000 }),
    makeTtsEngine({
      id: "fish_s2_pro",
      name: "Fish Speech S2 Pro",
      commercial_ok: false,
      license: "Fish Audio Research License",
    }),
    makeTtsEngine({
      id: "indextts2",
      name: "IndexTTS-2",
      commercial_ok: false,
      license: "IndexTTS Research / Non-Commercial",
    }),
  ];

  it("returns the selected engine in TTS mode and trims whitespace", () => {
    const wf = makeWorkflow();
    expect(
      resolveSelectedTtsEngine(
        wf,
        { "pipeline.audio_source": "tts", "pipeline.tts_engine": "qwen3_tts" },
        engines,
      )?.id,
    ).toBe("qwen3_tts");

    expect(
      resolveSelectedTtsEngine(
        wf,
        { "pipeline.audio_source": "tts", "pipeline.tts_engine": " indextts2 " },
        engines,
      )?.id,
    ).toBe("indextts2");
  });

  it("falls back to workflow default_tts_engine when pipeline.tts_engine is unset or blank", () => {
    const wf = makeWorkflow();
    expect(
      resolveSelectedTtsEngine(wf, { "pipeline.audio_source": "tts" }, engines)?.id,
    ).toBe("voxcpm2");
    expect(
      resolveSelectedTtsEngine(
        wf,
        { "pipeline.audio_source": "tts", "pipeline.tts_engine": "   " },
        engines,
      )?.id,
    ).toBe("voxcpm2");
  });

  it("returns undefined in Audio mode or for non-digital-human workflows", () => {
    const digitalHuman = makeWorkflow();
    expect(
      resolveSelectedTtsEngine(
        digitalHuman,
        { "pipeline.audio_source": "audio", "pipeline.tts_engine": "voxcpm2" },
        engines,
      ),
    ).toBeUndefined();

    const standardWf = makeWorkflow({
      pipeline: { kind: "comfy_standard" },
    });
    expect(
      resolveSelectedTtsEngine(
        standardWf,
        { "pipeline.audio_source": "tts", "pipeline.tts_engine": "voxcpm2" },
        engines,
      ),
    ).toBeUndefined();

    const noPipelineWf = makeWorkflow({ pipeline: null });
    expect(
      resolveSelectedTtsEngine(
        noPipelineWf,
        { "pipeline.audio_source": "tts", "pipeline.tts_engine": "voxcpm2" },
        engines,
      ),
    ).toBeUndefined();

    expect(resolveSelectedTtsEngine(undefined, {}, engines)).toBeUndefined();
  });

  it("returns undefined when engines list is empty or engine ID is unknown", () => {
    const wf = makeWorkflow();
    expect(
      resolveSelectedTtsEngine(wf, { "pipeline.audio_source": "tts" }, []),
    ).toBeUndefined();
    expect(
      resolveSelectedTtsEngine(wf, { "pipeline.audio_source": "tts" }, undefined),
    ).toBeUndefined();
    expect(
      resolveSelectedTtsEngine(
        wf,
        { "pipeline.audio_source": "tts", "pipeline.tts_engine": "nonexistent" },
        engines,
      ),
    ).toBeUndefined();
  });

  it("rejects non-tts audio sources after trimming and enforces supported_tts_engines when non-empty", () => {
    const restrictedWf = makeWorkflow({
      pipeline: {
        kind: "digital_human",
        video_backend: "infinitetalk",
        default_tts_engine: "voxcpm2",
        supported_tts_engines: ["voxcpm2", "qwen3_tts"],
      },
    });

    expect(
      resolveSelectedTtsEngine(
        restrictedWf,
        { "pipeline.audio_source": " audio ", "pipeline.tts_engine": "voxcpm2" },
        engines,
      ),
    ).toBeUndefined();

    expect(
      resolveSelectedTtsEngine(
        restrictedWf,
        { "pipeline.audio_source": "custom", "pipeline.tts_engine": "voxcpm2" },
        engines,
      ),
    ).toBeUndefined();

    // indextts2 exists in `engines`, but is not listed in workflow's supported_tts_engines
    expect(
      resolveSelectedTtsEngine(
        restrictedWf,
        { "pipeline.audio_source": " tts ", "pipeline.tts_engine": "indextts2" },
        engines,
      ),
    ).toBeUndefined();

    expect(
      resolveSelectedTtsEngine(
        restrictedWf,
        { "pipeline.audio_source": " tts ", "pipeline.tts_engine": "qwen3_tts" },
        engines,
      )?.id,
    ).toBe("qwen3_tts");
  });
});

describe("workflowModelsTotalBytes", () => {
  it("returns 0 when workflow or models are absent or empty", () => {
    expect(workflowModelsTotalBytes(undefined)).toBe(0);
    expect(workflowModelsTotalBytes(makeWorkflow({ models: undefined }))).toBe(0);
    expect(workflowModelsTotalBytes(makeWorkflow({ models: [] }))).toBe(0);
  });

  it("sums positive finite model sizes accurately and ignores invalid values", () => {
    const wf = makeWorkflow({
      models: [
        {
          repo: "Kijai/WanVideo_comfy_fp8_scaled",
          revision: "0".repeat(40),
          path: "I2V/model.safetensors",
          target_dir: "diffusion_models",
          size: 16_643_349_018,
          sha256: "a".repeat(64),
        },
        {
          repo: "Kijai/WanVideo_comfy",
          revision: "1".repeat(40),
          path: "umt5.safetensors",
          target_dir: "text_encoders",
          size: 6_731_333_792,
          sha256: "b".repeat(64),
        },
        {
          repo: "Phips/2xNomosUni_span_multijpg",
          revision: "2".repeat(40),
          path: "2xNomosUni_span_multijpg.safetensors",
          target_dir: "upscale_models",
          size: 4_461_056,
          sha256: "c".repeat(64),
        },
        {
          repo: "org/invalid",
          revision: "3".repeat(40),
          path: "neg.safetensors",
          target_dir: "vae",
          size: -100,
          sha256: "d".repeat(64),
        },
        {
          repo: "org/nan",
          revision: "4".repeat(40),
          path: "nan.safetensors",
          target_dir: "vae",
          size: Number.NaN,
          sha256: "e".repeat(64),
        },
      ],
    });

    expect(workflowModelsTotalBytes(wf)).toBe(
      16_643_349_018 + 6_731_333_792 + 4_461_056,
    );
  });
});

describe("hasNonCommercialNotice", () => {
  const voxcpm2 = makeTtsEngine({
    id: "voxcpm2",
    name: "VoxCPM2",
    license: "Apache-2.0",
    commercial_ok: true,
    license_notice: "",
  });
  const qwen3Tts = makeTtsEngine({
    id: "qwen3_tts",
    name: "Qwen3-TTS 1.7B",
    license: "Apache-2.0",
    commercial_ok: true,
    license_notice: "",
  });
  const fishS2Pro = makeTtsEngine({
    id: "fish_s2_pro",
    name: "Fish Speech S2 Pro",
    license: "Fish Audio Research License",
    commercial_ok: false,
    license_notice: "仅限个人学习与非商业研究使用；如需商业使用须另行向 Fish Audio 获取商业授权。",
  });
  const indextts2 = makeTtsEngine({
    id: "indextts2",
    name: "IndexTTS-2",
    license: "IndexTTS Research / Non-Commercial",
    commercial_ok: false,
    license_notice: "仅限个人学习与非商业研究使用；商用前请核查 IndexTeam 上游权重许可协议。",
  });

  it("returns null for commercial-OK workflows paired with Apache-2.0 engines or no TTS", () => {
    const commercialWf = makeWorkflow({ commercial_ok: true, license: "Apache-2.0" });
    expect(hasNonCommercialNotice(commercialWf, voxcpm2)).toBeNull();
    expect(hasNonCommercialNotice(commercialWf, qwen3Tts)).toBeNull();
    expect(hasNonCommercialNotice(commercialWf, undefined)).toBeNull();
    expect(hasNonCommercialNotice(undefined, undefined)).toBeNull();
  });

  it("returns warning details when selected TTS engine is non-commercial", () => {
    const commercialWf = makeWorkflow({ commercial_ok: true });

    expect(hasNonCommercialNotice(commercialWf, fishS2Pro)).toEqual({
      restrictedBy: "tts",
      items: [
        {
          kind: "tts",
          name: "Fish Speech S2 Pro",
          license: "Fish Audio Research License",
          notice:
            "仅限个人学习与非商业研究使用；如需商业使用须另行向 Fish Audio 获取商业授权。",
        },
      ],
      license: "Fish Audio Research License",
      notice: "仅限个人学习与非商业研究使用；如需商业使用须另行向 Fish Audio 获取商业授权。",
      sourceName: "Fish Speech S2 Pro",
    });

    expect(hasNonCommercialNotice(commercialWf, indextts2)).toEqual({
      restrictedBy: "tts",
      items: [
        {
          kind: "tts",
          name: "IndexTTS-2",
          license: "IndexTTS Research / Non-Commercial",
          notice:
            "仅限个人学习与非商业研究使用；商用前请核查 IndexTeam 上游权重许可协议。",
        },
      ],
      license: "IndexTTS Research / Non-Commercial",
      notice: "仅限个人学习与非商业研究使用；商用前请核查 IndexTeam 上游权重许可协议。",
      sourceName: "IndexTTS-2",
    });
  });

  it("returns warning details when workflow template itself is non-commercial", () => {
    const nonCommercialWf = makeWorkflow({
      id: "research_avatar",
      name: "Research Avatar Pack",
      commercial_ok: false,
      license: "CC-BY-NC-4.0",
      license_notice: "Non-commercial research use only.",
    });

    expect(hasNonCommercialNotice(nonCommercialWf, voxcpm2)).toEqual({
      restrictedBy: "workflow",
      items: [
        {
          kind: "workflow",
          name: "Research Avatar Pack",
          license: "CC-BY-NC-4.0",
          notice: "Non-commercial research use only.",
        },
      ],
      license: "CC-BY-NC-4.0",
      notice: "Non-commercial research use only.",
      sourceName: "Research Avatar Pack",
    });

    const unnamedWf = makeWorkflow({
      id: "unnamed_nc_wf",
      name: "   ",
      commercial_ok: false,
      license: "Research-Only",
      license_notice: "Research only.",
    });
    expect(hasNonCommercialNotice(unnamedWf, undefined)?.sourceName).toBe(
      "unnamed_nc_wf",
    );
  });

  it("merges notices when both workflow template and TTS engine are non-commercial", () => {
    const nonCommercialWf = makeWorkflow({
      name: "Research Video Model",
      commercial_ok: false,
      license: "CC-BY-NC-4.0",
      license_notice: "Video weights are non-commercial.",
    });

    expect(hasNonCommercialNotice(nonCommercialWf, fishS2Pro)).toEqual({
      restrictedBy: "both",
      items: [
        {
          kind: "workflow",
          name: "Research Video Model",
          license: "CC-BY-NC-4.0",
          notice: "Video weights are non-commercial.",
        },
        {
          kind: "tts",
          name: "Fish Speech S2 Pro",
          license: "Fish Audio Research License",
          notice:
            "仅限个人学习与非商业研究使用；如需商业使用须另行向 Fish Audio 获取商业授权。",
        },
      ],
      license: "CC-BY-NC-4.0 / Fish Audio Research License",
      notice:
        "Video weights are non-commercial. 仅限个人学习与非商业研究使用；如需商业使用须另行向 Fish Audio 获取商业授权。",
      sourceName: "Research Video Model + Fish Speech S2 Pro",
    });

    const sharedLicenseWf = makeWorkflow({
      name: "Shared License Pack",
      commercial_ok: false,
      license: fishS2Pro.license,
      license_notice: fishS2Pro.license_notice,
    });
    expect(hasNonCommercialNotice(sharedLicenseWf, fishS2Pro)).toEqual({
      restrictedBy: "both",
      items: [
        {
          kind: "workflow",
          name: "Shared License Pack",
          license: fishS2Pro.license,
          notice: fishS2Pro.license_notice,
        },
        {
          kind: "tts",
          name: "Fish Speech S2 Pro",
          license: fishS2Pro.license,
          notice: fishS2Pro.license_notice,
        },
      ],
      license: fishS2Pro.license,
      notice: fishS2Pro.license_notice,
      sourceName: "Shared License Pack + Fish Speech S2 Pro",
    });
  });

  it("returns workflow-only in uploaded audio mode and both with non-commercial TTS for upgraded digital-human-longcat", () => {
    const longCatNativeWf = makeWorkflow({
      id: "digital-human-longcat",
      name: "口播数字人 · 96GB 电影级旗舰版",
      source: "builtin",
      commercial_ok: false,
      license: "MIT / Apache-2.0（仅限非商用）",
      license_notice:
        "本旗舰数字人集成方案（LongCat-Video-Avatar 1.6 + SeedVR2 7B + FlashVSR+ 及内置旗舰 TTS）仅限个人学习、研究与非商用演示，严禁用于任何商业用途。",
      pipeline: {
        kind: "digital_human",
        video_backend: "longcat_avatar_native",
        default_tts_engine: "fish_drama3",
        supported_tts_engines: ["fish_drama3", "voxcpm2", "fish_s2_pro"],
      },
    });
    const fishDrama3 = makeTtsEngine({
      id: "fish_drama3",
      name: "Fish Audio Drama 3",
      license: "Fish Audio Community License v1.0",
      commercial_ok: false,
      license_notice:
        "仅限个人学习与非商业研究使用；严禁对外提供托管 TTS API 服务，商用须另行获取 Fish Audio 商业授权。",
    });
    const engines = [fishDrama3, voxcpm2, fishS2Pro];

    // Uploaded audio mode resolves no TTS engine, yet digital-human-longcat itself still triggers "workflow".
    const audioModeTts = resolveSelectedTtsEngine(
      longCatNativeWf,
      {
        "pipeline.audio_source": "audio",
        "pipeline.tts_engine": "fish_drama3",
      },
      engines,
    );
    expect(audioModeTts).toBeUndefined();
    expect(hasNonCommercialNotice(longCatNativeWf, audioModeTts)).toEqual({
      restrictedBy: "workflow",
      items: [
        {
          kind: "workflow",
          name: "口播数字人 · 96GB 电影级旗舰版",
          license: "MIT / Apache-2.0（仅限非商用）",
          notice:
            "本旗舰数字人集成方案（LongCat-Video-Avatar 1.6 + SeedVR2 7B + FlashVSR+ 及内置旗舰 TTS）仅限个人学习、研究与非商用演示，严禁用于任何商业用途。",
        },
      ],
      sourceName: "口播数字人 · 96GB 电影级旗舰版",
      license: "MIT / Apache-2.0（仅限非商用）",
      notice:
        "本旗舰数字人集成方案（LongCat-Video-Avatar 1.6 + SeedVR2 7B + FlashVSR+ 及内置旗舰 TTS）仅限个人学习、研究与非商用演示，严禁用于任何商业用途。",
    });

    // Commercial TTS (VoxCPM2) paired with non-commercial LongCat 1.6 still reports "workflow".
    expect(hasNonCommercialNotice(longCatNativeWf, voxcpm2)?.restrictedBy).toBe(
      "workflow",
    );

    // Non-commercial TTS (Fish Audio Drama 3) paired with non-commercial LongCat 1.6 reports "both".
    const ttsModeEngine = resolveSelectedTtsEngine(
      longCatNativeWf,
      {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "fish_drama3",
      },
      engines,
    );
    expect(ttsModeEngine).toBe(fishDrama3);
    expect(hasNonCommercialNotice(longCatNativeWf, ttsModeEngine)).toEqual({
      restrictedBy: "both",
      items: [
        {
          kind: "workflow",
          name: "口播数字人 · 96GB 电影级旗舰版",
          license: "MIT / Apache-2.0（仅限非商用）",
          notice:
            "本旗舰数字人集成方案（LongCat-Video-Avatar 1.6 + SeedVR2 7B + FlashVSR+ 及内置旗舰 TTS）仅限个人学习、研究与非商用演示，严禁用于任何商业用途。",
        },
        {
          kind: "tts",
          name: "Fish Audio Drama 3",
          license: "Fish Audio Community License v1.0",
          notice:
            "仅限个人学习与非商业研究使用；严禁对外提供托管 TTS API 服务，商用须另行获取 Fish Audio 商业授权。",
        },
      ],
      sourceName: "口播数字人 · 96GB 电影级旗舰版 + Fish Audio Drama 3",
      license:
        "MIT / Apache-2.0（仅限非商用） / Fish Audio Community License v1.0",
      notice:
        "本旗舰数字人集成方案（LongCat-Video-Avatar 1.6 + SeedVR2 7B + FlashVSR+ 及内置旗舰 TTS）仅限个人学习、研究与非商用演示，严禁用于任何商业用途。 仅限个人学习与非商业研究使用；严禁对外提供托管 TTS API 服务，商用须另行获取 Fish Audio 商业授权。",
    });
  });

  it("handles undefined commercial_ok fallbacks for longcat_avatar_native and built-in non-commercial TTS engines, plus pure-ASCII MIT / Apache-2.0 license", () => {
    const fallbackNativeWf = makeWorkflow({
      id: "digital-human-longcat",
      name: "LongCat 1.6 Native",
      license: "MIT / Apache-2.0",
      commercial_ok: undefined,
      license_notice: "",
      pipeline: {
        kind: "digital_human",
        video_backend: "longcat_avatar_native",
      },
    });
    const nativeOnly = hasNonCommercialNotice(fallbackNativeWf, voxcpm2);
    expect(nativeOnly).not.toBeNull();
    expect(nativeOnly?.restrictedBy).toBe("workflow");
    expect(nativeOnly?.sourceName).toBe("LongCat 1.6 Native");
    expect(nativeOnly?.license).toBe("MIT / Apache-2.0");
    expect(nativeOnly?.notice).toBe("");

    const commercialWf = makeWorkflow({
      id: "digital-human-infinitetalk",
      commercial_ok: true,
      license: "Apache-2.0",
    });
    for (const ncId of [
      "fish_drama3",
      "breeze_tts2",
      "indextts2",
      "fish_s2_pro",
    ] as const) {
      const fallbackTts = makeTtsEngine({
        id: ncId,
        name: `Engine ${ncId}`,
        license: "Community Research License",
        commercial_ok: undefined,
        license_notice: "",
      });
      const res = hasNonCommercialNotice(commercialWf, fallbackTts);
      expect(res, ncId).not.toBeNull();
      expect(res?.restrictedBy, ncId).toBe("tts");
      expect(res?.sourceName, ncId).toBe(`Engine ${ncId}`);
    }

    // Pure-ASCII MIT / Apache-2.0 license with explicit commercial_ok: false preserves license cleanly
    const asciiLicenseNativeWf = makeWorkflow({
      id: "digital-human-longcat",
      name: "口播数字人 · 96GB 电影级旗舰版",
      license: "MIT / Apache-2.0",
      commercial_ok: false,
      license_notice: "Non-commercial research and personal evaluation only.",
      pipeline: {
        kind: "digital_human",
        video_backend: "longcat_avatar_native",
      },
    });
    expect(hasNonCommercialNotice(asciiLicenseNativeWf, voxcpm2)).toEqual({
      restrictedBy: "workflow",
      items: [
        {
          kind: "workflow",
          name: "口播数字人 · 96GB 电影级旗舰版",
          license: "MIT / Apache-2.0",
          notice: "Non-commercial research and personal evaluation only.",
        },
      ],
      sourceName: "口播数字人 · 96GB 电影级旗舰版",
      license: "MIT / Apache-2.0",
      notice: "Non-commercial research and personal evaluation only.",
    });
  });
});

describe("missingRequiredWorkflowFields", () => {
  const digitalHumanFields: WorkflowField[] = [
    makeField({
      key: "1.image",
      label: "主播人像",
      field_type: "image",
      group: "media",
      required: true,
    }),
    makeField({
      key: "pipeline.audio_source",
      label: "音频来源",
      field_type: "select",
      group: "voice",
      default: "tts",
      options: ["tts", "audio"],
      required: true,
    }),
    makeField({
      key: "pipeline.script_text",
      label: "播报台词",
      field_type: "textarea",
      group: "voice",
      required: true,
      visible_when: { "pipeline.audio_source": "tts" },
    }),
    makeField({
      key: "2.audio",
      label: "驱动音频",
      field_type: "audio",
      group: "voice",
      required: true,
      visible_when: { "pipeline.audio_source": "audio" },
    }),
    makeField({
      key: "pipeline.voice_ref_audio",
      label: "音色参考音频",
      field_type: "audio",
      group: "voice",
      required: false,
      visible_when: { "pipeline.audio_source": "tts" },
    }),
    makeField({
      key: "8.steps",
      label: "采样步数",
      field_type: "integer",
      group: "advanced",
      required: true,
    }),
  ];

  it("returns empty array for undefined workflow or workflow without fields", () => {
    expect(missingRequiredWorkflowFields(undefined, {}, {}, {})).toEqual([]);
    expect(
      missingRequiredWorkflowFields(makeWorkflow({ fields: [] }), {}, {}, {}),
    ).toEqual([]);
  });

  it("checks only visible required fields in TTS vs Audio mode", () => {
    const wf = makeWorkflow({ fields: digitalHumanFields });

    // TTS mode: 2.audio is hidden so only 1.image, pipeline.script_text, 8.steps are missing
    const missingInTts = missingRequiredWorkflowFields(
      wf,
      { "pipeline.audio_source": "tts", "pipeline.script_text": "   " },
      {},
      {},
    );
    expect(missingInTts.map((f) => f.key)).toEqual([
      "1.image",
      "pipeline.script_text",
      "8.steps",
    ]);

    // Audio mode: pipeline.script_text is hidden, 2.audio becomes visible and required
    const missingInAudio = missingRequiredWorkflowFields(
      wf,
      { "pipeline.audio_source": "audio", "8.steps": 4 },
      {},
      {},
    );
    expect(missingInAudio.map((f) => f.key)).toEqual(["1.image", "2.audio"]);
  });

  it("respects uploadedInputIds and pendingUploads for required image/audio fields", () => {
    const wf = makeWorkflow({ fields: digitalHumanFields });

    // Image satisfied by uploadedInputIds, audio satisfied by pendingUploads
    const noneMissing = missingRequiredWorkflowFields(
      wf,
      { "pipeline.audio_source": "audio", "8.steps": 0 },
      { "1.image": "input_img_01" },
      { "2.audio": { fileName: "voice.wav" } },
    );
    expect(noneMissing).toEqual([]);

    // Whitespace-only uploadedInputIds without pending upload is still missing
    const whitespaceUpload = missingRequiredWorkflowFields(
      wf,
      { "pipeline.audio_source": "audio", "8.steps": 4 },
      { "1.image": "   ", "2.audio": "input_aud_01" },
      {},
    );
    expect(whitespaceUpload.map((f) => f.key)).toEqual(["1.image"]);
  });

  it("validates required number/integer and boolean fields strictly", () => {
    const wf = makeWorkflow({
      fields: [
        makeField({ key: "3.cfg", field_type: "number", required: true }),
        makeField({ key: "3.flag", field_type: "boolean", required: true }),
      ],
    });

    expect(
      missingRequiredWorkflowFields(
        wf,
        { "3.cfg": "", "3.flag": "true" },
        {},
        {},
      ).map((f) => f.key),
    ).toEqual(["3.cfg", "3.flag"]);

    expect(
      missingRequiredWorkflowFields(
        wf,
        { "3.cfg": Number.NaN, "3.flag": false },
        {},
        {},
      ).map((f) => f.key),
    ).toEqual(["3.cfg"]);

    expect(
      missingRequiredWorkflowFields(
        wf,
        { "3.cfg": 0, "3.flag": false },
        {},
        {},
      ),
    ).toEqual([]);
  });

  it("requires pipeline.voice_ref_audio when digital_human TTS engine resolves to indextts2", () => {
    const wf = makeWorkflow({ fields: digitalHumanFields });

    // indextts2 explicitly selected in TTS mode -> voice_ref_audio is required even though field.required === false
    const missingForIndexTts = missingRequiredWorkflowFields(
      wf,
      {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "indextts2",
        "pipeline.script_text": "hello world",
        "8.steps": 6,
      },
      { "1.image": "img_01" },
      {},
    );
    expect(missingForIndexTts.map((f) => f.key)).toEqual([
      "pipeline.voice_ref_audio",
    ]);

    // Default TTS engine fallback to indextts2 also triggers requirement
    const defaultIndexTtsWf = makeWorkflow({
      fields: digitalHumanFields,
      pipeline: {
        kind: "digital_human",
        video_backend: "infinitetalk",
        default_tts_engine: "indextts2",
        supported_tts_engines: ["indextts2", "voxcpm2"],
      },
    });
    expect(
      missingRequiredWorkflowFields(
        defaultIndexTtsWf,
        {
          "pipeline.audio_source": " tts ",
          "pipeline.script_text": "hello world",
          "8.steps": 6,
        },
        { "1.image": "img_01" },
        {},
      ).map((f) => f.key),
    ).toEqual(["pipeline.voice_ref_audio"]);

    // Satisfied when voice_ref_audio is uploaded or staged as pending
    expect(
      missingRequiredWorkflowFields(
        wf,
        {
          "pipeline.audio_source": "tts",
          "pipeline.tts_engine": "indextts2",
          "pipeline.script_text": "hello world",
          "8.steps": 6,
        },
        { "1.image": "img_01", "pipeline.voice_ref_audio": "aud_ref_01" },
        {},
      ),
    ).toEqual([]);
    expect(
      missingRequiredWorkflowFields(
        wf,
        {
          "pipeline.audio_source": "tts",
          "pipeline.tts_engine": "indextts2",
          "pipeline.script_text": "hello world",
          "8.steps": 6,
        },
        { "1.image": "img_01" },
        { "pipeline.voice_ref_audio": { filename: "ref.wav" } },
      ),
    ).toEqual([]);

    // Other TTS engines keep voice_ref_audio optional
    expect(
      missingRequiredWorkflowFields(
        wf,
        {
          "pipeline.audio_source": "tts",
          "pipeline.tts_engine": "voxcpm2",
          "pipeline.script_text": "hello world",
          "8.steps": 6,
        },
        { "1.image": "img_01" },
        {},
      ),
    ).toEqual([]);

    // Audio mode does not require voice_ref_audio even if tts_engine stays "indextts2"
    expect(
      missingRequiredWorkflowFields(
        wf,
        {
          "pipeline.audio_source": "audio",
          "pipeline.tts_engine": "indextts2",
          "8.steps": 6,
        },
        { "1.image": "img_01", "2.audio": "aud_01" },
        {},
      ),
    ).toEqual([]);
  });

  it("allows indextts2 without uploaded voice_ref_audio when built-in voice_preset is selected and requires it only for custom", () => {
    const voicePresetField = makeField({
      key: "pipeline.voice_preset",
      label: "内置女声音色",
      field_type: "select",
      group: "basic",
      default: "shen_zhiyi",
      options: [
        "shen_zhiyi",
        "lin_xiaoman",
        "su_wanqing",
        "gu_qinglan",
        "lu_mingwei",
        "custom",
      ],
      required: false,
      visible_when: { "pipeline.audio_source": "tts" },
    });
    const presetWf = makeWorkflow({
      fields: [...digitalHumanFields, voicePresetField],
    });
    const legacyWf = makeWorkflow({ fields: digitalHumanFields });

    // Direct helper checks: legacy workflow without pipeline.voice_preset stays required
    expect(isColabOneVoiceRefRequired(undefined, {})).toBe(false);
    expect(
      isColabOneVoiceRefRequired(
        makeWorkflow({ pipeline: { kind: "comfy_standard" } }),
        { "pipeline.audio_source": "tts", "pipeline.tts_engine": "indextts2" },
      ),
    ).toBe(false);
    expect(
      isColabOneVoiceRefRequired(legacyWf, {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "indextts2",
      }),
    ).toBe(true);
    expect(
      isColabOneVoiceRefRequired(legacyWf, {
        "pipeline.audio_source": "audio",
        "pipeline.tts_engine": "indextts2",
      }),
    ).toBe(false);

    // Built-in named presets (and unset fallback to default "shen_zhiyi") do NOT require uploaded voice_ref_audio
    for (const presetId of [
      "shen_zhiyi",
      "lin_xiaoman",
      "su_wanqing",
      "gu_qinglan",
      "lu_mingwei",
      "",
    ]) {
      const vals = {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "indextts2",
        "pipeline.voice_preset": presetId,
        "pipeline.script_text": "hello world",
        "8.steps": 8,
      };
      expect(isColabOneVoiceRefRequired(presetWf, vals)).toBe(false);
      expect(
        missingRequiredWorkflowFields(presetWf, vals, { "1.image": "img_01" }, {}),
      ).toEqual([]);
    }

    // Omitting pipeline.voice_preset when field default is "shen_zhiyi" does NOT require voice_ref_audio
    expect(
      isColabOneVoiceRefRequired(presetWf, {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "indextts2",
      }),
    ).toBe(false);

    // Selecting "custom" (including padded whitespace) requires pipeline.voice_ref_audio for indextts2
    for (const customPreset of ["custom", " custom "]) {
      const customVals = {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "indextts2",
        "pipeline.voice_preset": customPreset,
        "pipeline.script_text": "hello world",
        "8.steps": 8,
      };
      expect(isColabOneVoiceRefRequired(presetWf, customVals)).toBe(true);
      expect(
        missingRequiredWorkflowFields(
          presetWf,
          customVals,
          { "1.image": "img_01" },
          {},
        ).map((f) => f.key),
      ).toEqual(["pipeline.voice_ref_audio"]);

      // Satisfied once uploaded or staged as pending
      expect(
        missingRequiredWorkflowFields(
          presetWf,
          customVals,
          { "1.image": "img_01", "pipeline.voice_ref_audio": "aud_ref_01" },
          {},
        ),
      ).toEqual([]);
      expect(
        missingRequiredWorkflowFields(
          presetWf,
          customVals,
          { "1.image": "img_01" },
          { "pipeline.voice_ref_audio": { filename: "ref.wav" } },
        ),
      ).toEqual([]);
    }

    // Other clone-only engines (fish_drama3, moss_tts_v1_5, fish_s2_pro) also require voice_ref_audio under "custom"
    for (const engineId of ["fish_drama3", "moss_tts_v1_5", "fish_s2_pro"]) {
      const customVals = {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": engineId,
        "pipeline.voice_preset": "custom",
        "pipeline.script_text": "hello world",
        "8.steps": 8,
      };
      expect(isColabOneVoiceRefRequired(presetWf, customVals)).toBe(true);
      expect(
        missingRequiredWorkflowFields(
          presetWf,
          customVals,
          { "1.image": "img_01" },
          {},
        ).map((f) => f.key),
      ).toEqual(["pipeline.voice_ref_audio"]);

      const presetVals = {
        ...customVals,
        "pipeline.voice_preset": "shen_zhiyi",
      };
      expect(isColabOneVoiceRefRequired(presetWf, presetVals)).toBe(false);
      expect(
        missingRequiredWorkflowFields(
          presetWf,
          presetVals,
          { "1.image": "img_01" },
          {},
        ),
      ).toEqual([]);
    }

    // Voice-design engines do NOT require voice_ref_audio even under "custom"
    for (const engineId of ["voxcpm2", "qwen3_tts", "breeze_tts2", "ming_3_tts"]) {
      const vals = {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": engineId,
        "pipeline.voice_preset": "custom",
        "pipeline.script_text": "hello world",
        "8.steps": 8,
      };
      expect(isColabOneVoiceRefRequired(presetWf, vals)).toBe(false);
      expect(
        missingRequiredWorkflowFields(presetWf, vals, { "1.image": "img_01" }, {}),
      ).toEqual([]);
    }

    // Audio mode never requires voice_ref_audio even under indextts2 + custom
    expect(
      isColabOneVoiceRefRequired(presetWf, {
        "pipeline.audio_source": "audio",
        "pipeline.tts_engine": "indextts2",
        "pipeline.voice_preset": "custom",
      }),
    ).toBe(false);

    // If a template explicitly defaults pipeline.voice_preset to "custom", omitting the value still requires ref audio
    const customDefaultWf = makeWorkflow({
      fields: [
        ...digitalHumanFields,
        makeField({
          key: "pipeline.voice_preset",
          field_type: "select",
          default: "custom",
          options: ["custom", "shen_zhiyi"],
          visible_when: { "pipeline.audio_source": "tts" },
        }),
      ],
    });
    expect(
      isColabOneVoiceRefRequired(customDefaultWf, {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "indextts2",
      }),
    ).toBe(true);
  });
});

describe("buildColabOneWorkflowOverrides", () => {
  const fields: WorkflowField[] = [
    makeField({
      key: "1.image",
      field_type: "image",
      required: true,
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
      visible_when: { "pipeline.audio_source": "tts" },
    }),
    makeField({
      key: "pipeline.voice_ref_audio",
      field_type: "audio",
      visible_when: { "pipeline.audio_source": "tts" },
    }),
    makeField({
      key: "2.audio",
      field_type: "audio",
      visible_when: { "pipeline.audio_source": "audio" },
    }),
    makeField({
      key: "8.steps",
      field_type: "integer",
      default: 6,
      min_value: 1,
    }),
    makeField({
      key: "8.seed",
      field_type: "integer",
      min_value: 42,
    }),
    makeField({
      key: "8.cfg",
      field_type: "number",
      default: 1.5,
      min_value: 0.5,
    }),
    makeField({
      key: "8.scale",
      field_type: "number",
    }),
    makeField({
      key: "8.enhance",
      field_type: "boolean",
      default: false,
    }),
  ];

  it("trims strings, rounds integers, rejects NaN/Infinity/boolean on numeric fields, and skips hidden fields", () => {
    const wf = makeWorkflow({ fields });

    const payload = buildColabOneWorkflowOverrides(
      wf,
      {
        "1.image": "  portrait.png  ",
        "pipeline.audio_source": " tts ",
        "pipeline.script_text": "  hello digital human  ",
        "pipeline.voice_ref_audio": "  ref.wav  ",
        "2.audio": "stale_audio.wav",
        "8.steps": 4.6,
        "8.seed": true,
        "8.cfg": Number.NaN,
        "8.scale": Number.POSITIVE_INFINITY,
        "8.enhance": "false",
      },
      wf.fields,
      {
        uploadedInputIds: {
          "1.image": "  a".repeat(1) + "b".repeat(31) + "  ",
          "2.audio": "c".repeat(32),
        },
        pendingUploadKeys: {
          "pipeline.voice_ref_audio": { filename: "ref.wav" },
        },
      },
    );

    expect(payload.inputs).toEqual({
      "1.image": `a${"b".repeat(31)}`,
    });
    expect(payload.overrides).toEqual({
      "pipeline.audio_source": "tts",
      "pipeline.script_text": "hello digital human",
      "8.steps": 5,
      "8.seed": 42,
      "8.cfg": 1.5,
      "8.scale": 0,
      "8.enhance": false,
    });
    // Hidden audio field excluded from both overrides and inputs; pending voice ref not leaked into overrides
    expect("2.audio" in payload.overrides).toBe(false);
    expect("2.audio" in payload.inputs).toBe(false);
    expect("pipeline.voice_ref_audio" in payload.overrides).toBe(false);
  });

  it("omits TTS fields when switched to audio mode and never emits NaN or null", () => {
    const wf = makeWorkflow({ fields });

    const payload = buildColabOneWorkflowOverrides(
      wf,
      {
        "1.image": "  remote_portrait.png  ",
        "pipeline.audio_source": "audio",
        "pipeline.script_text": "stale script",
        "8.steps": "",
        "8.seed": Number.NEGATIVE_INFINITY,
        "8.cfg": null as unknown as number,
        "8.scale": false,
        "8.enhance": "1",
      },
      undefined,
      {
        uploadedInputIds: {
          "2.audio": "d".repeat(32),
          "pipeline.voice_ref_audio": "e".repeat(32),
        },
      },
    );

    expect(payload.inputs).toEqual({
      "2.audio": "d".repeat(32),
    });
    expect(payload.overrides).toEqual({
      "1.image": "remote_portrait.png",
      "pipeline.audio_source": "audio",
      "8.steps": 6,
      "8.seed": 42,
      "8.cfg": 1.5,
      "8.scale": 0,
      "8.enhance": true,
    });
    expect("pipeline.script_text" in payload.overrides).toBe(false);
    expect("pipeline.voice_ref_audio" in payload.inputs).toBe(false);

    for (const val of Object.values(payload.overrides)) {
      expect(val).not.toBeNull();
      expect(val).not.toBeUndefined();
      if (typeof val === "number") {
        expect(Number.isFinite(val)).toBe(true);
      }
    }
  });

  it("returns empty payload for undefined workflow", () => {
    expect(buildColabOneWorkflowOverrides(undefined, {})).toEqual({
      overrides: {},
      inputs: {},
    });
  });

  it("serializes pipeline.voice_preset, pipeline.render_quality, and pipeline.acceleration_profile with defaults and visibility rules", () => {
    const longcatFields: WorkflowField[] = [
      makeField({
        key: "pipeline.audio_source",
        field_type: "select",
        default: "tts",
        options: ["tts", "audio"],
      }),
      makeField({
        key: "pipeline.voice_preset",
        field_type: "select",
        group: "basic",
        default: "shen_zhiyi",
        options: [
          "shen_zhiyi",
          "lin_xiaoman",
          "su_wanqing",
          "gu_qinglan",
          "lu_mingwei",
          "custom",
        ],
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "pipeline.render_quality",
        field_type: "select",
        group: "advanced",
        default: "balanced_480p_ai2x",
        options: ["balanced_480p_ai2x", "native_720p"],
        visible_when: null,
      }),
      makeField({
        key: "pipeline.acceleration_profile",
        field_type: "select",
        group: "advanced",
        default: "max",
        options: ["max", "balanced", "compat"],
        visible_when: null,
      }),
    ];
    const wf = makeWorkflow({
      id: "digital-human-longcat",
      fields: longcatFields,
      pipeline: {
        kind: "digital_human",
        video_backend: "longcat_avatar",
        default_tts_engine: "voxcpm2",
      },
    });

    // Explicit trimmed values in TTS mode
    const explicit = buildColabOneWorkflowOverrides(wf, {
      "pipeline.audio_source": "tts",
      "pipeline.voice_preset": " lin_xiaoman ",
      "pipeline.render_quality": " native_720p ",
      "pipeline.acceleration_profile": " balanced ",
    });
    expect(explicit.overrides).toEqual({
      "pipeline.audio_source": "tts",
      "pipeline.voice_preset": "lin_xiaoman",
      "pipeline.render_quality": "native_720p",
      "pipeline.acceleration_profile": "balanced",
    });

    // Blank / unset values fall back to field defaults
    const defaulted = buildColabOneWorkflowOverrides(wf, {
      "pipeline.audio_source": "tts",
      "pipeline.voice_preset": "   ",
      "pipeline.render_quality": "",
    });
    expect(defaulted.overrides).toEqual({
      "pipeline.audio_source": "tts",
      "pipeline.voice_preset": "shen_zhiyi",
      "pipeline.render_quality": "balanced_480p_ai2x",
      "pipeline.acceleration_profile": "max",
    });

    // Audio mode hides pipeline.voice_preset while preserving render_quality & acceleration_profile
    const audioMode = buildColabOneWorkflowOverrides(wf, {
      "pipeline.audio_source": "audio",
      "pipeline.voice_preset": "gu_qinglan",
      "pipeline.render_quality": "balanced_480p_ai2x",
      "pipeline.acceleration_profile": "compat",
    });
    expect(audioMode.overrides).toEqual({
      "pipeline.audio_source": "audio",
      "pipeline.render_quality": "balanced_480p_ai2x",
      "pipeline.acceleration_profile": "compat",
    });
    expect("pipeline.voice_preset" in audioMode.overrides).toBe(false);
  });

  it("omits hidden LongCat 1.6 sampling-mode controls, falls back unknown/whitespace quality_preset to non_distill_cfg, and validates voice_preset regex and select options", () => {
    const nativeFields: WorkflowField[] = [
      makeField({
        key: "pipeline.audio_source",
        field_type: "select",
        default: "tts",
        options: ["tts", "audio"],
      }),
      makeField({
        key: "pipeline.voice_preset",
        field_type: "select",
        default: "shen_zhiyi",
        options: ["shen_zhiyi", "lin_xiaoman", "custom"],
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "pipeline.quality_preset",
        field_type: "select",
        default: "cinema_flagship",
        options: ["cinema_flagship", "balanced_hd", "longform_fast"],
      }),
      makeField({
        key: "pipeline.sampling_mode",
        field_type: "select",
        default: "auto",
        options: ["auto", "non_distill_cfg", "distill_lora_mix"],
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
        key: "pipeline.num_inference_steps",
        field_type: "integer",
        default: 50,
        min_value: 8,
        max_value: 60,
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
        key: "pipeline.no_default_select",
        field_type: "select",
        options: ["opt_a", "opt_b"],
      }),
    ];
    const nativeWf = makeWorkflow({
      id: "digital-human-longcat",
      fields: nativeFields,
      pipeline: {
        kind: "digital_human",
        video_backend: "longcat_avatar_native",
      },
    });

    // 1. Unknown/whitespace quality_preset with auto sampling falls back to non_distill_cfg
    for (const rawPreset of ["   ", "nonexistent_preset"]) {
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.quality_preset": rawPreset,
          "pipeline.sampling_mode": "auto",
        }),
      ).toBe("non_distill_cfg");

      const cfgPayload = buildColabOneWorkflowOverrides(nativeWf, {
        "pipeline.audio_source": "tts",
        "pipeline.quality_preset": rawPreset,
        "pipeline.sampling_mode": "auto",
        "pipeline.audio_guidance_scale": 5.0,
        "pipeline.text_guidance_scale": 3.5,
        "pipeline.num_inference_steps": 45,
        "pipeline.mix_steps": 3,
        "pipeline.mix_alpha": 0.7,
      });
      expect(cfgPayload.overrides["pipeline.quality_preset"]).toBe("cinema_flagship");
      expect(cfgPayload.overrides["pipeline.audio_guidance_scale"]).toBe(5.0);
      expect(cfgPayload.overrides["pipeline.text_guidance_scale"]).toBe(3.5);
      expect(cfgPayload.overrides["pipeline.num_inference_steps"]).toBe(45);
      expect("pipeline.mix_steps" in cfgPayload.overrides).toBe(false);
      expect("pipeline.mix_alpha" in cfgPayload.overrides).toBe(false);
    }

    // 2. distill_lora_mix hides & omits CFG/steps while serializing mix_steps & mix_alpha
    const loraPayload = buildColabOneWorkflowOverrides(nativeWf, {
      "pipeline.audio_source": "tts",
      "pipeline.quality_preset": "balanced_hd",
      "pipeline.sampling_mode": "auto",
      "pipeline.audio_guidance_scale": 5.5,
      "pipeline.num_inference_steps": 40,
      "pipeline.mix_steps": 3,
      "pipeline.mix_alpha": 0.85,
    });
    expect(loraPayload.overrides["pipeline.mix_steps"]).toBe(3);
    expect(loraPayload.overrides["pipeline.mix_alpha"]).toBe(0.85);
    expect("pipeline.audio_guidance_scale" in loraPayload.overrides).toBe(false);
    expect("pipeline.text_guidance_scale" in loraPayload.overrides).toBe(false);
    expect("pipeline.num_inference_steps" in loraPayload.overrides).toBe(false);

    // 3. voice_preset regex (/^[a-zA-Z0-9_-]{1,64}$/) accepts valid dynamic IDs and rejects malformed ones
    expect(COLABONE_VOICE_PRESET_ID_RE.test("qwen_vivian")).toBe(true);
    expect(COLABONE_VOICE_PRESET_ID_RE.test("A".repeat(64))).toBe(true);
    expect(COLABONE_VOICE_PRESET_ID_RE.test("A".repeat(65))).toBe(false);
    expect(COLABONE_VOICE_PRESET_ID_RE.test("../escape")).toBe(false);
    expect(COLABONE_VOICE_PRESET_ID_RE.test("bad preset")).toBe(false);

    const validDynamic = buildColabOneWorkflowOverrides(nativeWf, {
      "pipeline.audio_source": "tts",
      "pipeline.voice_preset": `  ${"Voice_01-a".padEnd(64, "z")}  `,
      "pipeline.no_default_select": " opt_b ",
    });
    expect(validDynamic.overrides["pipeline.voice_preset"]).toBe(
      "Voice_01-a".padEnd(64, "z"),
    );
    expect(validDynamic.overrides["pipeline.no_default_select"]).toBe("opt_b");

    for (const badVoice of [
      "../etc/passwd",
      "voice with spaces",
      "bad$char",
      "a".repeat(65),
      true as unknown as string,
    ]) {
      const invalidVoicePayload = buildColabOneWorkflowOverrides(nativeWf, {
        "pipeline.audio_source": "tts",
        "pipeline.voice_preset": badVoice,
        "pipeline.sampling_mode": "invalid_mode",
        "pipeline.no_default_select": "unknown_opt",
      });
      expect(invalidVoicePayload.overrides["pipeline.voice_preset"]).toBe("shen_zhiyi");
      expect(invalidVoicePayload.overrides["pipeline.sampling_mode"]).toBe("auto");
      expect(invalidVoicePayload.overrides["pipeline.no_default_select"]).toBe("opt_a");
    }
  });
});

describe("ColabOne narrowed ComfyUI & TaskStatus types", () => {
  it("supports ColabOne* exported types, legacy aliases, output extension union, and TaskStatus logs", () => {
    const patch: ColabOneComfyNodePatch = {
      node_name: "ComfyUI-WanVideoWrapper",
      kind: "wav2vec2_hidden_states_hook",
    };
    const legacyPatch: ComfyNodePatch = patch;
    const pipeline: ColabOneComfyPipelineSpec = {
      kind: "digital_human",
      video_backend: "longcat_avatar",
    };
    const legacyPipeline: ComfyPipelineSpec = pipeline;
    const voicePreset: ColabOneComfyTtsVoicePreset = {
      id: "shen_zhiyi",
      name: "知性女主播 · 沈知意",
      gender: "female",
      style: "知性主播",
      description: "清澈温润的现代标准普通话",
      prompt: "A 26-year-old Chinese female host",
      seed: 202601,
    };
    const legacyVoicePreset: ComfyTtsVoicePreset = voicePreset;
    const tts: ColabOneComfyTtsEngineSpec = makeTtsEngine({
      id: "voxcpm2",
      voice_presets: [voicePreset],
    });
    const legacyTts: ComfyTtsEngineSpec = tts;
    const output: WorkflowOutput = {
      id: "a".repeat(32),
      workflow_id: "digital-human-longcat",
      workflow_name: "LongCat",
      kind: "video",
      filename: "out.mp4",
      extension: "mp4",
      mime: "video/mp4",
      size: 1024,
      created_at: 1_700_000_000,
      elapsed: 12.5,
    };
    const task: TaskStatus = {
      id: "t1",
      kind: "comfyui_run",
      model_id: "",
      state: "running",
      progress: 0.5,
      message: "running",
      error: "",
      created_at: 1_700_000_000,
      logs: [{ seq: 1, time: 1_700_000_001, message: "started", level: "info" }],
    };

    expect(legacyPatch.kind).toBe("wav2vec2_hidden_states_hook");
    expect(legacyPipeline.kind).toBe("digital_human");
    expect(legacyVoicePreset.id).toBe("shen_zhiyi");
    expect(legacyVoicePreset.gender).toBe("female");
    expect(legacyTts.id).toBe("voxcpm2");
    expect(legacyTts.voice_presets?.[0].seed).toBe(202601);
    expect(output.extension).toBe("mp4");
    expect(task.logs?.[0].seq).toBe(1);
  });
});

describe("localized workflow defaults, preset visual prompt, and placeholders", () => {
  const ALL_LOCALES: ColabOneLocale[] = [
    "zh-CN",
    "en-US",
    "zh-TW",
    "zh-HK",
    "ja-JP",
    "ko-KR",
  ];

  const visualPromptField = makeField({
    key: "5.positive_prompt",
    label: "画面风格提示词",
    node_id: "5",
    input_name: "positive_prompt",
    field_type: "textarea",
    default: COLABONE_CANONICAL_VISUAL_PROMPT,
  });

  it("localizes canonical visual prompt in defaultFieldValues and resolveColabOneWorkflowFieldDefault for all 6 locales", () => {
    expect(COLABONE_LOCALE_LIST.map((m) => m.code)).toEqual(ALL_LOCALES);

    for (const locale of ALL_LOCALES) {
      const expected = t("comfy.defaultVisualPrompt", undefined, locale);
      expect(expected.trim().length).toBeGreaterThan(0);

      expect(
        resolveColabOneWorkflowFieldDefault(visualPromptField, locale),
      ).toBe(expected);

      const values = defaultFieldValues(
        [
          visualPromptField,
          makeField({
            key: "pipeline.script_text",
            field_type: "textarea",
            default: "",
          }),
          makeField({
            key: "custom.text",
            field_type: "text",
            default: "custom non-preset default",
          }),
        ],
        locale,
      );
      expect(values["5.positive_prompt"]).toBe(expected);
      expect(values["pipeline.script_text"]).toBe("");
      expect(values["custom.text"]).toBe("custom non-preset default");
    }
  });

  it("recognizes canonical, all 6 localized, and all 6 legacy preset visual prompts and rejects custom/empty/non-string values", () => {
    expect(COLABONE_CANONICAL_VISUAL_PROMPT).toBe(
      "Static medium shot of an engaging presenter speaking warmly and naturally to the camera, making expressive, natural hand gestures and open-palm movements to emphasize points while talking, lively facial expressions, subtle eyebrow raises, natural head tilts, shoulder rhythm, natural blinking, clear lip articulation synced with speech, soft studio lighting, sharp focus, stable background",
    );
    expect(COLABONE_CANONICAL_VISUAL_PROMPT).toBe(
      t("comfy.defaultVisualPrompt", undefined, "en-US"),
    );
    expect(COLABONE_LEGACY_VISUAL_PROMPTS.size).toBe(6);
    expect(isColabOnePresetVisualPrompt(COLABONE_CANONICAL_VISUAL_PROMPT)).toBe(
      true,
    );
    expect(
      isColabOnePresetVisualPrompt(`  ${COLABONE_CANONICAL_VISUAL_PROMPT}  `),
    ).toBe(true);

    for (const locale of ALL_LOCALES) {
      const localized = t("comfy.defaultVisualPrompt", undefined, locale);
      expect(isColabOnePresetVisualPrompt(localized)).toBe(true);
      expect(isColabOnePresetVisualPrompt(`  ${localized}\n`)).toBe(true);
    }

    for (const legacyPrompt of COLABONE_LEGACY_VISUAL_PROMPTS) {
      expect(isColabOnePresetVisualPrompt(legacyPrompt)).toBe(true);
      expect(isColabOnePresetVisualPrompt(`  ${legacyPrompt}  `)).toBe(true);
    }

    expect(isColabOnePresetVisualPrompt("")).toBe(false);
    expect(isColabOnePresetVisualPrompt("   ")).toBe(false);
    expect(isColabOnePresetVisualPrompt("A custom studio portrait")).toBe(false);
    expect(isColabOnePresetVisualPrompt(`${COLABONE_CANONICAL_VISUAL_PROMPT}!`)).toBe(
      false,
    );
    expect(isColabOnePresetVisualPrompt(null)).toBe(false);
    expect(isColabOnePresetVisualPrompt(undefined)).toBe(false);
    expect(isColabOnePresetVisualPrompt(42)).toBe(false);
    expect(isColabOnePresetVisualPrompt(true)).toBe(false);
  });

  it("upgrades stale unedited legacy visual prompt drafts on locale remap and default resolution without touching custom prompts", () => {
    const legacyEnPrompt =
      "A clear studio portrait of a speaker talking naturally to the camera with expressive facial movements, gentle head motion, natural blinking, sharp focus, soft studio lighting, stable background";
    const legacyZhCnPrompt =
      "清晰演播室人像镜头，主播面向镜头自然说话，面部表情生动自然，头部轻微摆动，自然眨眼，焦距锐利，柔和演播室灯光，背景稳定不抖动";
    const legacyZhTwPrompt =
      "清晰攝影棚人像鏡頭，主播面向鏡頭自然說話，臉部表情生動自然，頭部輕微擺動，自然眨眼，焦距銳利，柔和攝影棚燈光，背景穩定不晃動";
    const legacyZhHkPrompt =
      "清晰錄影廠人像鏡頭，主播面向鏡頭自然說話，面部表情生動自然，頭部輕微擺動，自然眨眼，焦距銳利，柔和錄影廠燈光，背景穩定不晃動";
    const legacyJaPrompt =
      "鮮明なスタジオのポートレート映像。カメラに向かって自然に話す話者、豊かな表情、穏やかな頭の動き、自然なまばたき、シャープなピント、柔らかなスタジオ照明、安定した背景";
    const legacyKoPrompt =
      "선명한 스튜디오 인물 샷, 카메라를 향해 자연스럽게 말하는 진행자, 생동감 있는 얼굴 표정, 부드러운 머리 움직임, 자연스러운 눈 깜빡임, 또렷한 초점, 부드러운 스튜디오 조명, 안정적인 배경";

    const fields: WorkflowField[] = [
      visualPromptField,
      makeField({
        key: "pipeline.script_text",
        field_type: "textarea",
        default: "",
      }),
    ];

    for (const legacyPrompt of [
      legacyEnPrompt,
      legacyZhCnPrompt,
      legacyZhTwPrompt,
      legacyZhHkPrompt,
      legacyJaPrompt,
      legacyKoPrompt,
    ]) {
      for (const targetLocale of ALL_LOCALES) {
        const upgraded = remapColabOnePresetPromptFieldsForLocale(
          fields,
          {
            "5.positive_prompt": legacyPrompt,
            "pipeline.script_text": "keep my script",
          },
          targetLocale,
        );
        expect(upgraded["5.positive_prompt"]).toBe(
          t("comfy.defaultVisualPrompt", undefined, targetLocale),
        );
        expect(upgraded["5.positive_prompt"]).not.toBe(legacyPrompt);
        expect(upgraded["pipeline.script_text"]).toBe("keep my script");
      }
    }

    // Legacy template field default also resolves to the new localized visual prompt
    const legacyField = makeField({
      key: "5.positive_prompt",
      field_type: "textarea",
      default: legacyEnPrompt,
    });
    expect(resolveColabOneWorkflowFieldDefault(legacyField, "en-US")).toBe(
      COLABONE_CANONICAL_VISUAL_PROMPT,
    );
    expect(resolveColabOneWorkflowFieldDefault(legacyField, "zh-CN")).toBe(
      t("comfy.defaultVisualPrompt", undefined, "zh-CN"),
    );

    // Custom user prompt that starts similarly to legacy text must NOT be upgraded
    const customUntouched = remapColabOnePresetPromptFieldsForLocale(
      fields,
      { "5.positive_prompt": `${legacyEnPrompt}, dramatic neon backdrop` },
      "zh-CN",
    );
    expect(customUntouched["5.positive_prompt"]).toBe(
      `${legacyEnPrompt}, dramatic neon backdrop`,
    );
  });

  it("remaps untouched preset prompt across locales while preserving user-edited and explicitly cleared values", () => {
    const fields: WorkflowField[] = [
      visualPromptField,
      makeField({
        key: "pipeline.script_text",
        field_type: "textarea",
        default: "",
      }),
    ];

    const original = {
      "5.positive_prompt": t("comfy.defaultVisualPrompt", undefined, "zh-CN"),
      "pipeline.script_text": "hello world",
    };
    const remapped = remapColabOnePresetPromptFieldsForLocale(
      fields,
      original,
      "ja-JP",
    );
    expect(remapped["5.positive_prompt"]).toBe(
      t("comfy.defaultVisualPrompt", undefined, "ja-JP"),
    );
    expect(remapped["pipeline.script_text"]).toBe("hello world");
    // Does not mutate input
    expect(original["5.positive_prompt"]).toBe(
      t("comfy.defaultVisualPrompt", undefined, "zh-CN"),
    );

    // User-edited prompt must never be overwritten
    const edited = remapColabOnePresetPromptFieldsForLocale(
      fields,
      {
        "5.positive_prompt": "My custom cinematic lighting prompt",
        "pipeline.script_text": "script",
      },
      "en-US",
    );
    expect(edited["5.positive_prompt"]).toBe(
      "My custom cinematic lighting prompt",
    );

    // Explicitly cleared empty string must never be overwritten
    const cleared = remapColabOnePresetPromptFieldsForLocale(
      fields,
      {
        "5.positive_prompt": "",
      },
      "ko-KR",
    );
    expect(cleared["5.positive_prompt"]).toBe("");

    // Handles undefined fields safely
    expect(
      remapColabOnePresetPromptFieldsForLocale(
        undefined,
        { "5.positive_prompt": COLABONE_CANONICAL_VISUAL_PROMPT },
        "zh-HK",
      ),
    ).toEqual({ "5.positive_prompt": COLABONE_CANONICAL_VISUAL_PROMPT });
  });

  it("resolves localized placeholders for builtin fields, custom workflow heuristics, and non-text fields", () => {
    for (const locale of ALL_LOCALES) {
      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({ key: "pipeline.script_text", field_type: "textarea" }),
          locale,
        ),
      ).toBe(t("comfy.placeholderScriptText", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({ key: "pipeline.voice_ref_text", field_type: "textarea" }),
          locale,
        ),
      ).toBe(t("comfy.placeholderVoiceRefText", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({ key: "pipeline.voice_prompt", field_type: "text" }),
          locale,
        ),
      ).toBe(t("comfy.placeholderVoicePrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({ key: "pipeline.emotion_prompt", field_type: "text" }),
          locale,
        ),
      ).toBe(t("comfy.placeholderEmotionPrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({ key: "5.positive_prompt", field_type: "textarea" }),
          locale,
        ),
      ).toBe(t("comfy.placeholderVisualPrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({ key: "pipeline.positive_prompt", field_type: "textarea" }),
          locale,
        ),
      ).toBe(t("comfy.placeholderVisualPrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({ key: "pipeline.negative_prompt", field_type: "textarea" }),
          locale,
        ),
      ).toBe(t("comfy.placeholderNegativePrompt", undefined, locale));

      // Any field whose default is the preset visual prompt gets placeholderVisualPrompt
      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "99.custom_prompt",
            input_name: "text",
            label: "Prompt",
            field_type: "textarea",
            default: COLABONE_CANONICAL_VISUAL_PROMPT,
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderVisualPrompt", undefined, locale));

      // Custom workflow heuristics: negative before prompt
      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "7.negative_prompt",
            input_name: "negative_prompt",
            label: "Negative Prompt",
            field_type: "textarea",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderNegativePrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "7.text",
            input_name: "text",
            label: "反向提示词",
            field_type: "textarea",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderNegativePrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "7.neg_cn",
            input_name: "text",
            label: "负向描述",
            field_type: "text",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderNegativePrompt", undefined, locale));

      // Custom positive / generic prompt
      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "6.positive_prompt",
            input_name: "positive_prompt",
            label: "Positive",
            field_type: "textarea",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderGenericPrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "6.zh_prompt",
            input_name: "text",
            label: "正向提示詞",
            field_type: "textarea",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderGenericPrompt", undefined, locale));

      // Japanese & Korean negative/positive/generic prompt keyword heuristics
      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "7.ja_neg",
            input_name: "text",
            label: "ネガティブプロンプト",
            field_type: "textarea",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderNegativePrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "7.ko_neg",
            input_name: "text",
            label: "네거티브 프롬프트",
            field_type: "textarea",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderNegativePrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "6.ja_pos",
            input_name: "text",
            label: "ポジティブプロンプト",
            field_type: "textarea",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderGenericPrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "6.ja_generic",
            input_name: "text",
            label: "シーンプロンプト",
            field_type: "text",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderGenericPrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "6.ko_pos",
            input_name: "text",
            label: "포지티브 프롬프트",
            field_type: "textarea",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderGenericPrompt", undefined, locale));

      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "6.ko_generic",
            input_name: "text",
            label: "장면 프롬프트",
            field_type: "text",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderGenericPrompt", undefined, locale));

      // Generic text fallback
      expect(
        resolveColabOneWorkflowFieldPlaceholder(
          makeField({
            key: "12.filename_prefix",
            input_name: "filename_prefix",
            label: "Output Prefix",
            field_type: "text",
          }),
          locale,
        ),
      ).toBe(t("comfy.placeholderGenericText", undefined, locale));

      // Non-text/textarea field types return ""
      for (const nonTextType of [
        "select",
        "number",
        "integer",
        "boolean",
        "image",
        "audio",
      ] as const) {
        expect(
          resolveColabOneWorkflowFieldPlaceholder(
            makeField({
              key: "pipeline.script_text",
              input_name: "positive_prompt",
              label: "Prompt",
              field_type: nonTextType,
            }),
            locale,
          ),
        ).toBe("");
      }
    }
  });
});

describe("unified digital human workflow helpers", () => {
  function makeSharedDigitalHumanFields(): WorkflowField[] {
    return [
      makeField({
        key: "1.image",
        label: "主播人像照片",
        field_type: "image",
        default: "",
        group: "basic",
        required: true,
      }),
      makeField({
        key: "pipeline.audio_source",
        label: "配音方式",
        field_type: "select",
        default: "tts",
        options: ["tts", "audio"],
        group: "basic",
        required: true,
      }),
      makeField({
        key: "pipeline.script_text",
        label: "口播文稿",
        field_type: "textarea",
        default: "",
        group: "basic",
        required: true,
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "pipeline.tts_engine",
        label: "远端 TTS 语音引擎",
        field_type: "select",
        default: "fish_drama3",
        options: [
          "fish_drama3",
          "moss_tts_v1_5",
          "breeze_tts2",
          "ming_3_tts",
          "voxcpm2",
          "qwen3_tts",
          "indextts2",
          "fish_s2_pro",
        ],
        group: "basic",
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "pipeline.voice_preset",
        label: "内置音色与试听",
        field_type: "select",
        default: "shen_zhiyi",
        options: [
          "shen_zhiyi",
          "lin_xiaoman",
          "su_wanqing",
          "gu_qinglan",
          "lu_mingwei",
          "chen_jingzhou",
          "lin_chenxu",
          "zhou_moyan",
          "gu_zhengyuan",
          "su_hechuan",
          "custom",
        ],
        group: "basic",
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "2.audio",
        label: "驱动音频文件",
        field_type: "audio",
        default: "",
        group: "basic",
        required: true,
        visible_when: { "pipeline.audio_source": "audio" },
      }),
      makeField({
        key: "pipeline.voice_ref_audio",
        label: "参考音色（可选）",
        field_type: "audio",
        default: "",
        group: "basic",
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "pipeline.target_resolution",
        label: "输出画幅与清晰度",
        field_type: "select",
        default: "1280x720",
        options: ["1280x720", "720x1280", "1920x1080", "1080x1920"],
        group: "basic",
      }),
      makeField({
        key: "pipeline.voice_ref_text",
        label: "参考音色对应文字（可选）",
        field_type: "text",
        default: "",
        group: "advanced",
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "pipeline.voice_prompt",
        label: "声线与风格描述",
        field_type: "text",
        default: "",
        group: "advanced",
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "pipeline.emotion_ref_audio",
        label: "情绪参考音频",
        field_type: "audio",
        default: "",
        group: "advanced",
        visible_when: {
          "pipeline.audio_source": "tts",
          "pipeline.tts_engine": "indextts2",
        },
      }),
      makeField({
        key: "pipeline.emotion_prompt",
        label: "情绪提示词",
        field_type: "text",
        default: "",
        group: "advanced",
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "pipeline.pause_seconds",
        label: "句间自然停顿（秒）",
        field_type: "number",
        default: 0.28,
        min_value: 0,
        max_value: 1.5,
        step: 0.02,
        group: "advanced",
        visible_when: { "pipeline.audio_source": "tts" },
      }),
      makeField({
        key: "5.positive_prompt",
        label: "画面与动作风格提示词",
        field_type: "textarea",
        default: COLABONE_CANONICAL_VISUAL_PROMPT,
        group: "advanced",
      }),
    ];
  }

  function makeInfiniteTalkWorkflow(
    overrides: Partial<WorkflowTemplate> = {},
  ): WorkflowTemplate {
    return makeWorkflow({
      id: "digital-human-infinitetalk",
      name: "口播数字人 · 长视频推荐版",
      source: "builtin",
      min_vram_mib: 40960,
      recommended_gpu: "G4 96 GB / A100 80 GB",
      pipeline: {
        kind: "digital_human",
        video_backend: "infinitetalk",
        default_tts_engine: "fish_drama3",
        max_segment_seconds: 20,
        upscale_mode: "ai_2x",
        steps_node_key: "15.steps",
        seed_node_key: "15.seed",
      },
      fields: [
        ...makeSharedDigitalHumanFields(),
        makeField({
          key: "pipeline.upscale_mode",
          label: "画质超分模式",
          field_type: "select",
          default: "ai_2x",
          options: ["ai_2x", "lanczos", "none"],
          group: "advanced",
        }),
        makeField({
          key: "pipeline.max_segment_seconds",
          label: "单段最大时长（秒）",
          field_type: "number",
          default: 20,
          min_value: 10,
          max_value: 45,
          step: 5,
          group: "advanced",
        }),
        makeField({
          key: "15.steps",
          label: "采样步数（LightX2V 蒸馏）",
          field_type: "integer",
          default: 8,
          min_value: 4,
          max_value: 20,
          step: 1,
          group: "advanced",
        }),
        makeField({
          key: "15.seed",
          label: "随机种子",
          field_type: "integer",
          default: 42,
          min_value: 0,
          max_value: 2147483647,
          step: 1,
          group: "advanced",
        }),
      ],
      ...overrides,
    });
  }

  function makeLongCatWorkflow(
    overrides: Partial<WorkflowTemplate> = {},
  ): WorkflowTemplate {
    return makeWorkflow({
      id: "digital-human-longcat",
      name: "口播数字人 · 旗舰高清版",
      source: "builtin",
      min_vram_mib: 49152,
      recommended_gpu: "G4 96 GB / A100 80 GB / H100 80 GB",
      pipeline: {
        kind: "digital_human",
        video_backend: "longcat_avatar",
        default_tts_engine: "fish_drama3",
        max_segment_seconds: 3.7,
        upscale_mode: "ai_2x",
        steps_node_key: "14.steps",
        seed_node_key: "15.seed",
      },
      fields: [
        ...makeSharedDigitalHumanFields(),
        makeField({
          key: "pipeline.render_quality",
          label: "渲染清晰度与速度模式",
          field_type: "select",
          default: "balanced_480p_ai2x",
          options: ["balanced_480p_ai2x", "native_720p"],
          group: "advanced",
        }),
        makeField({
          key: "pipeline.acceleration_profile",
          label: "远端 GPU 加速栈",
          field_type: "select",
          default: "max",
          options: ["max", "balanced", "compat"],
          group: "advanced",
        }),
        makeField({
          key: "pipeline.upscale_mode",
          label: "画质超分模式",
          field_type: "select",
          default: "ai_2x",
          options: ["ai_2x", "lanczos", "none"],
          group: "advanced",
        }),
        makeField({
          key: "pipeline.max_segment_seconds",
          label: "单段最大时长（秒）",
          field_type: "number",
          default: 3.7,
          min_value: 3.0,
          max_value: 9.0,
          step: 0.1,
          group: "advanced",
        }),
        makeField({
          key: "14.steps",
          label: "采样步数（LongCat DMD 蒸馏）",
          field_type: "integer",
          default: 8,
          min_value: 4,
          max_value: 20,
          step: 1,
          group: "advanced",
        }),
        makeField({
          key: "15.seed",
          label: "随机种子",
          field_type: "integer",
          default: 42,
          min_value: 0,
          max_value: 2147483647,
          step: 1,
          group: "advanced",
        }),
      ],
      ...overrides,
    });
  }

  function makeLongCatNativeWorkflow(
    overrides: Partial<WorkflowTemplate> = {},
  ): WorkflowTemplate {
    const sharedWithoutPrompt = makeSharedDigitalHumanFields().filter(
      (field) =>
        field.key !== "5.positive_prompt" &&
        field.key !== "pipeline.target_resolution",
    );
    return makeWorkflow({
      id: "digital-human-longcat",
      name: "口播数字人 · 96GB 电影级旗舰版",
      source: "builtin",
      min_vram_mib: 49152,
      min_disk_mib: 73728,
      recommended_gpu: "G4 96 GB / H100 80 GB / A100 80 GB",
      license: "MIT / Apache-2.0（仅限非商用）",
      commercial_ok: false,
      license_notice:
        "本旗舰数字人集成方案（LongCat-Video-Avatar 1.6 + SeedVR2 7B + FlashVSR+ 及内置旗舰 TTS）仅限个人学习、研究与非商用演示，严禁用于任何商业用途。",
      pipeline: {
        kind: "digital_human",
        video_backend: "longcat_avatar_native",
        default_tts_engine: "fish_drama3",
        max_segment_seconds: 3.72,
        upscale_mode: "auto",
        prompt_node_key: "pipeline.positive_prompt",
      },
      fields: [
        ...sharedWithoutPrompt,
        makeField({
          key: "pipeline.quality_preset",
          label: "画质与渲染模式",
          field_type: "select",
          default: "cinema_flagship",
          options: ["cinema_flagship", "balanced_hd", "longform_fast"],
          group: "basic",
        }),
        makeField({
          key: "pipeline.framing_style",
          label: "动作与景别风格",
          field_type: "select",
          default: "half_body_gestures",
          options: [
            "half_body_gestures",
            "closeup_presenter",
            "full_body_dynamic",
          ],
          group: "basic",
        }),
        makeField({
          key: "pipeline.upscale_mode",
          label: "牙齿与质感超分引擎",
          field_type: "select",
          default: "auto",
          options: [
            "auto",
            "seedvr2_7b_sharp",
            "seedvr2_7b",
            "flashvsr_plus",
            "none",
          ],
          group: "basic",
        }),
        makeField({
          key: "pipeline.target_resolution",
          label: "输出画幅与清晰度",
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
          group: "basic",
        }),
        makeField({
          key: "pipeline.sampling_mode",
          label: "采样推理路径",
          field_type: "select",
          default: "auto",
          options: ["auto", "non_distill_cfg", "distill_lora_mix"],
          group: "advanced",
        }),
        makeField({
          key: "pipeline.audio_guidance_scale",
          label: "口型贴合度（Audio CFG）",
          field_type: "number",
          default: 4.0,
          min_value: 1.0,
          max_value: 7.0,
          step: 0.1,
          group: "advanced",
        }),
        makeField({
          key: "pipeline.text_guidance_scale",
          label: "画面提示词引导（Text CFG）",
          field_type: "number",
          default: 4.0,
          min_value: 1.0,
          max_value: 7.0,
          step: 0.1,
          group: "advanced",
        }),
        makeField({
          key: "pipeline.num_inference_steps",
          label: "非蒸馏采样步数",
          field_type: "integer",
          default: 50,
          min_value: 8,
          max_value: 60,
          step: 1,
          group: "advanced",
        }),
        makeField({
          key: "pipeline.mix_steps",
          label: "双 LoRA 前段 DMD 步数",
          field_type: "integer",
          default: 4,
          min_value: 0,
          max_value: 8,
          step: 1,
          group: "advanced",
        }),
        makeField({
          key: "pipeline.mix_alpha",
          label: "双 LoRA DMD 混合权重",
          field_type: "number",
          default: 1.0,
          min_value: 0.0,
          max_value: 1.0,
          step: 0.05,
          group: "advanced",
        }),
        makeField({
          key: "pipeline.vocal_separation",
          label: "前置人声分离（Kim_Vocal_2）",
          field_type: "select",
          default: "auto",
          options: ["auto", "always", "never"],
          group: "advanced",
        }),
        makeField({
          key: "pipeline.ref_img_index",
          label: "身份参考帧索引",
          field_type: "integer",
          default: 10,
          min_value: 0,
          max_value: 30,
          step: 1,
          group: "advanced",
        }),
        makeField({
          key: "pipeline.seed",
          label: "随机种子",
          field_type: "integer",
          default: 42,
          min_value: 0,
          max_value: 2147483647,
          step: 1,
          group: "advanced",
        }),
        makeField({
          key: "pipeline.crf",
          label: "终稿编码画质（CRF，越小越清晰）",
          field_type: "integer",
          default: 16,
          min_value: 12,
          max_value: 24,
          step: 1,
          group: "advanced",
        }),
        makeField({
          key: "pipeline.positive_prompt",
          label: "自定义画面与动作提示词（可选）",
          field_type: "textarea",
          default: "",
          group: "advanced",
        }),
        makeField({
          key: "pipeline.negative_prompt",
          label: "自定义负向提示词（可选）",
          field_type: "textarea",
          default: "",
          group: "advanced",
        }),
      ],
      ...overrides,
    });
  }

  function makeMockStorage(initial: Record<string, string> = {}): Storage {
    const store = new Map<string, string>(Object.entries(initial));
    return {
      get length() {
        return store.size;
      },
      clear() {
        store.clear();
      },
      getItem(key: string) {
        return store.has(key) ? (store.get(key) ?? null) : null;
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

  function makeThrowingStorage(): Storage {
    const fail = (): never => {
      throw new Error("storage unavailable");
    };
    return {
      get length() {
        return 0;
      },
      clear: fail,
      getItem: fail,
      key: () => null,
      removeItem: fail,
      setItem: fail,
    };
  }

  describe("constants and isColabOneBuiltinDigitalHumanWorkflow", () => {
    it("exports canonical group key and engine-specific field key set", () => {
      expect(COLABONE_DIGITAL_HUMAN_GROUP_KEY).toBe(
        "colabone-digital-human-group",
      );
      const expectedLegacyEngineKeys = [
        "pipeline.max_segment_seconds",
        "pipeline.render_quality",
        "pipeline.acceleration_profile",
        "14.steps",
        "15.steps",
      ];
      const expectedLongCatNativeKeys = [
        "pipeline.quality_preset",
        "pipeline.framing_style",
        "pipeline.sampling_mode",
        "pipeline.audio_guidance_scale",
        "pipeline.text_guidance_scale",
        "pipeline.num_inference_steps",
        "pipeline.mix_steps",
        "pipeline.mix_alpha",
        "pipeline.vocal_separation",
        "pipeline.ref_img_index",
        "pipeline.seed",
        "pipeline.crf",
        "pipeline.positive_prompt",
        "pipeline.negative_prompt",
      ];
      expect(COLABONE_DIGITAL_HUMAN_ENGINE_SPECIFIC_KEYS.size).toBe(19);
      for (const key of [
        ...expectedLegacyEngineKeys,
        ...expectedLongCatNativeKeys,
      ]) {
        expect(
          COLABONE_DIGITAL_HUMAN_ENGINE_SPECIFIC_KEYS.has(key),
          `expected engine-specific key ${key}`,
        ).toBe(true);
      }
      for (const sharedKey of [
        "1.image",
        "2.audio",
        "15.seed",
        "pipeline.upscale_mode",
        "pipeline.target_resolution",
        "pipeline.script_text",
        "pipeline.tts_engine",
        "pipeline.voice_preset",
      ]) {
        expect(
          COLABONE_DIGITAL_HUMAN_ENGINE_SPECIFIC_KEYS.has(sharedKey),
          `expected shared key ${sharedKey} to stay transferable`,
        ).toBe(false);
      }
    });

    it("resolves LongCat 1.6 native sampling mode from explicit override or quality_preset", () => {
      // Explicit sampling_mode always takes precedence over quality_preset (with trimming)
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.sampling_mode": "non_distill_cfg",
          "pipeline.quality_preset": "balanced_hd",
        }),
      ).toBe("non_distill_cfg");
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.sampling_mode": "  non_distill_cfg  ",
          "pipeline.quality_preset": "longform_fast",
        }),
      ).toBe("non_distill_cfg");
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.sampling_mode": "distill_lora_mix",
          "pipeline.quality_preset": "cinema_flagship",
        }),
      ).toBe("distill_lora_mix");
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.sampling_mode": "  distill_lora_mix  ",
          "pipeline.quality_preset": "cinema_flagship",
        }),
      ).toBe("distill_lora_mix");

      // "auto" and omitted/unknown sampling_mode derive from quality_preset
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.sampling_mode": "auto",
          "pipeline.quality_preset": "cinema_flagship",
        }),
      ).toBe("non_distill_cfg");
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.sampling_mode": "auto",
          "pipeline.quality_preset": "balanced_hd",
        }),
      ).toBe("distill_lora_mix");
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.sampling_mode": "auto",
          "pipeline.quality_preset": "longform_fast",
        }),
      ).toBe("distill_lora_mix");
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.quality_preset": "balanced_hd",
        }),
      ).toBe("distill_lora_mix");
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.quality_preset": "longform_fast",
        }),
      ).toBe("distill_lora_mix");
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.sampling_mode": "unknown_mode",
          "pipeline.quality_preset": "balanced_hd",
        }),
      ).toBe("distill_lora_mix");

      // Empty/omitted quality_preset defaults to cinema_flagship non_distill_cfg
      expect(resolveColabOneLongCatNativeSamplingMode({})).toBe(
        "non_distill_cfg",
      );
      expect(resolveColabOneLongCatNativeSamplingMode(undefined)).toBe(
        "non_distill_cfg",
      );
      expect(resolveColabOneLongCatNativeSamplingMode(null)).toBe(
        "non_distill_cfg",
      );
      expect(
        resolveColabOneLongCatNativeSamplingMode({
          "pipeline.sampling_mode": "auto",
          "pipeline.quality_preset": "   ",
        }),
      ).toBe("non_distill_cfg");
    });

    it("matches only builtin workflows whose pipeline.kind is digital_human", () => {
      expect(
        isColabOneBuiltinDigitalHumanWorkflow(makeInfiniteTalkWorkflow()),
      ).toBe(true);
      expect(
        isColabOneBuiltinDigitalHumanWorkflow(makeLongCatWorkflow()),
      ).toBe(true);
      expect(
        isColabOneBuiltinDigitalHumanWorkflow(
          makeInfiniteTalkWorkflow({ source: "custom", id: "custom-dh" }),
        ),
      ).toBe(false);
      expect(
        isColabOneBuiltinDigitalHumanWorkflow(
          makeWorkflow({
            id: "builtin-std",
            source: "builtin",
            pipeline: { kind: "comfy_standard" },
          }),
        ),
      ).toBe(false);
      expect(
        isColabOneBuiltinDigitalHumanWorkflow(
          makeWorkflow({
            id: "builtin-none",
            source: "builtin",
            pipeline: null,
          }),
        ),
      ).toBe(false);
      expect(isColabOneBuiltinDigitalHumanWorkflow(undefined)).toBe(false);
      expect(isColabOneBuiltinDigitalHumanWorkflow(null)).toBe(false);
    });
  });

  describe("buildColabOneStudioWorkflowEntries", () => {
    it("returns an empty array for empty, undefined, or null workflow lists", () => {
      expect(buildColabOneStudioWorkflowEntries([])).toEqual([]);
      expect(buildColabOneStudioWorkflowEntries(undefined)).toEqual([]);
      expect(buildColabOneStudioWorkflowEntries(null)).toEqual([]);
    });

    it("merges builtin digital human workflows into one grouped entry while preserving custom workflows in relative order", () => {
      const customBefore = makeWorkflow({
        id: "custom-before",
        name: "Custom Portrait",
        source: "custom",
        pipeline: { kind: "comfy_standard" },
      });
      const infiniteTalk = makeInfiniteTalkWorkflow();
      const customDigitalHuman = makeWorkflow({
        id: "custom-digital-human",
        name: "Custom Digital Human",
        source: "custom",
        pipeline: {
          kind: "digital_human",
          video_backend: "custom",
        },
      });
      const longCat = makeLongCatWorkflow();
      const builtinStandard = makeWorkflow({
        id: "builtin-image-gen",
        name: "Builtin Standard",
        source: "builtin",
        pipeline: { kind: "comfy_standard" },
      });
      const customAfter = makeWorkflow({
        id: "custom-after",
        name: "Custom Video",
        source: "custom",
        pipeline: null,
      });

      const entries = buildColabOneStudioWorkflowEntries([
        customBefore,
        infiniteTalk,
        customDigitalHuman,
        longCat,
        builtinStandard,
        customAfter,
      ]);

      expect(entries.map((entry) => entry.key)).toEqual([
        "custom-before",
        COLABONE_DIGITAL_HUMAN_GROUP_KEY,
        "custom-digital-human",
        "builtin-image-gen",
        "custom-after",
      ]);

      expect(entries[0]).toEqual({
        key: "custom-before",
        isGroup: false,
        workflows: [customBefore],
        primary: customBefore,
      });
      expect(entries[1]).toEqual({
        key: COLABONE_DIGITAL_HUMAN_GROUP_KEY,
        isGroup: true,
        workflows: [infiniteTalk, longCat],
        primary: infiniteTalk,
      });
      expect(entries[2]).toEqual({
        key: "custom-digital-human",
        isGroup: false,
        workflows: [customDigitalHuman],
        primary: customDigitalHuman,
      });
      expect(entries[3]).toEqual({
        key: "builtin-image-gen",
        isGroup: false,
        workflows: [builtinStandard],
        primary: builtinStandard,
      });
      expect(entries[4]).toEqual({
        key: "custom-after",
        isGroup: false,
        workflows: [customAfter],
        primary: customAfter,
      });
    });

    it("leaves a single built-in digital human workflow ungrouped for backward compatibility", () => {
      const infiniteTalk = makeInfiniteTalkWorkflow();
      const customDigitalHuman = makeWorkflow({
        id: "custom-dh-only",
        source: "custom",
        pipeline: { kind: "digital_human", video_backend: "custom" },
      });
      const customOther = makeWorkflow({
        id: "custom-other",
        source: "custom",
        pipeline: null,
      });

      const entries = buildColabOneStudioWorkflowEntries([
        infiniteTalk,
        customDigitalHuman,
        customOther,
      ]);

      expect(entries).toEqual([
        {
          key: "digital-human-infinitetalk",
          isGroup: false,
          workflows: [infiniteTalk],
          primary: infiniteTalk,
        },
        {
          key: "custom-dh-only",
          isGroup: false,
          workflows: [customDigitalHuman],
          primary: customDigitalHuman,
        },
        {
          key: "custom-other",
          isGroup: false,
          workflows: [customOther],
          primary: customOther,
        },
      ]);

      const longCatOnly = makeLongCatWorkflow();
      expect(buildColabOneStudioWorkflowEntries([longCatOnly])).toEqual([
        {
          key: "digital-human-longcat",
          isGroup: false,
          workflows: [longCatOnly],
          primary: longCatOnly,
        },
      ]);
    });

    it("deduplicates duplicate built-in digital human IDs and namespaces colliding custom workflow IDs", () => {
      const infiniteTalkA = makeInfiniteTalkWorkflow();
      const infiniteTalkDup = makeInfiniteTalkWorkflow();
      const dupEntries = buildColabOneStudioWorkflowEntries([
        infiniteTalkA,
        infiniteTalkDup,
      ]);
      expect(dupEntries).toHaveLength(2);
      expect(dupEntries.every((entry) => entry.isGroup === false)).toBe(true);

      const longCat = makeLongCatWorkflow();
      const collidingCustom = makeWorkflow({
        id: COLABONE_DIGITAL_HUMAN_GROUP_KEY,
        name: "Custom Colliding ID",
        source: "custom",
        pipeline: null,
      });
      const mixedEntries = buildColabOneStudioWorkflowEntries([
        infiniteTalkA,
        longCat,
        collidingCustom,
      ]);
      expect(mixedEntries.map((entry) => entry.key)).toEqual([
        COLABONE_DIGITAL_HUMAN_GROUP_KEY,
        `custom:${COLABONE_DIGITAL_HUMAN_GROUP_KEY}`,
      ]);
      expect(mixedEntries[0].isGroup).toBe(true);
      expect(mixedEntries[0].workflows.map((wf) => wf.id)).toEqual([
        "digital-human-infinitetalk",
        "digital-human-longcat",
      ]);
      expect(mixedEntries[1].isGroup).toBe(false);
      expect(mixedEntries[1].primary).toBe(collidingCustom);
    });
  });

  describe("resolveColabOneDigitalHumanGpuContext and resolveColabOneRecommendedDigitalHumanWorkflowId", () => {
    const pair = [makeInfiniteTalkWorkflow(), makeLongCatWorkflow()];
    const reversedPair = [makeLongCatWorkflow(), makeInfiniteTalkWorkflow()];

    it("recommends LongCat for live GPUs with at least 48 GB VRAM (G4 96 GB, H100/A100 80 GB, exact 48 GB)", () => {
      const cases: Array<{
        label: string;
        runtime: RuntimeStatus;
        expectedMib: number;
      }> = [
        {
          label: "live G4 96 GB",
          runtime: {
            state: "ready",
            gpu: {
              name: "NVIDIA RTX PRO 6000 Blackwell",
              total_mib: 98304,
              used_mib: 512,
            },
          },
          expectedMib: 98304,
        },
        {
          label: "live H100 80 GB",
          runtime: {
            state: "ready",
            gpu: {
              name: "NVIDIA H100 80GB HBM3",
              total_mib: 81920,
              used_mib: 1024,
            },
          },
          expectedMib: 81920,
        },
        {
          label: "live A100 80 GB",
          runtime: {
            state: "ready",
            gpu: {
              name: "NVIDIA A100-SXM4-80GB",
              total_mib: 81920,
              used_mib: 1024,
            },
          },
          expectedMib: 81920,
        },
        {
          label: "live exact 48 GB boundary",
          runtime: {
            state: "ready",
            gpu: {
              name: "NVIDIA L40S",
              total_mib: 49152,
              used_mib: 0,
            },
          },
          expectedMib: 49152,
        },
      ];

      for (const tc of cases) {
        const ctx = resolveColabOneDigitalHumanGpuContext(tc.runtime, "T4");
        expect(ctx.isLive, tc.label).toBe(true);
        expect(ctx.vramMib, tc.label).toBe(tc.expectedMib);
        expect(ctx.shortLabel.trim().length, tc.label).toBeGreaterThan(0);

        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            pair,
            tc.runtime,
            "T4",
          ),
          tc.label,
        ).toBe("digital-human-longcat");
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            reversedPair,
            tc.runtime,
            "T4",
          ),
          `${tc.label} (reversed order)`,
        ).toBe("digital-human-longcat");
      }
    });

    it("recommends InfiniteTalk for live GPUs below 48 GB VRAM (A100 40 GB, L4 24 GB, T4 15 GB, and 49151 MiB)", () => {
      const cases: Array<{
        label: string;
        runtime: RuntimeStatus;
        expectedMib: number;
      }> = [
        {
          label: "live just below 48 GB",
          runtime: {
            state: "ready",
            gpu: {
              name: "NVIDIA L40S",
              total_mib: 49151,
              used_mib: 0,
            },
          },
          expectedMib: 49151,
        },
        {
          label: "live A100 40 GB",
          runtime: {
            state: "ready",
            gpu: {
              name: "NVIDIA A100-SXM4-40GB",
              total_mib: 40960,
              used_mib: 512,
            },
          },
          expectedMib: 40960,
        },
        {
          label: "live L4 24 GB",
          runtime: {
            state: "ready",
            gpu: {
              name: "NVIDIA L4",
              total_mib: 24576,
              used_mib: 256,
            },
          },
          expectedMib: 24576,
        },
        {
          label: "live T4 15 GB",
          runtime: {
            state: "ready",
            gpu: {
              name: "Tesla T4",
              total_mib: 15360,
              used_mib: 128,
            },
          },
          expectedMib: 15360,
        },
      ];

      for (const tc of cases) {
        const ctx = resolveColabOneDigitalHumanGpuContext(tc.runtime, "G4");
        expect(ctx.isLive, tc.label).toBe(true);
        expect(ctx.vramMib, tc.label).toBe(tc.expectedMib);
        expect(ctx.shortLabel.trim().length, tc.label).toBeGreaterThan(0);

        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            pair,
            tc.runtime,
            "G4",
          ),
          tc.label,
        ).toBe("digital-human-infinitetalk");
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            reversedPair,
            tc.runtime,
            "G4",
          ),
          `${tc.label} (reversed order)`,
        ).toBe("digital-human-infinitetalk");
      }
    });

    it("reads live VRAM from runtime.resources.gpu.total_mib when runtime.gpu is absent or empty", () => {
      const resourcesHighRuntime: RuntimeStatus = {
        state: "ready",
        resources: {
          gpu: {
            name: "NVIDIA RTX PRO 6000 Blackwell",
            total_mib: 98304,
            used_mib: 2048,
          },
        },
      };
      const highCtx = resolveColabOneDigitalHumanGpuContext(
        resourcesHighRuntime,
        "T4",
      );
      expect(highCtx.isLive).toBe(true);
      expect(highCtx.vramMib).toBe(98304);
      expect(highCtx.shortLabel.trim().length).toBeGreaterThan(0);
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          pair,
          resourcesHighRuntime,
          "T4",
        ),
      ).toBe("digital-human-longcat");

      const resourcesLowRuntime: RuntimeStatus = {
        state: "ready",
        resources: {
          gpu: {
            name: "NVIDIA L4",
            total_mib: 24576,
            used_mib: 1024,
          },
        },
      };
      const lowCtx = resolveColabOneDigitalHumanGpuContext(
        resourcesLowRuntime,
        "G4",
      );
      expect(lowCtx.isLive).toBe(true);
      expect(lowCtx.vramMib).toBe(24576);
      expect(lowCtx.shortLabel.trim().length).toBeGreaterThan(0);
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          pair,
          resourcesLowRuntime,
          "G4",
        ),
      ).toBe("digital-human-infinitetalk");

      const zeroPrimaryRuntime: RuntimeStatus = {
        state: "ready",
        gpu: {
          name: "",
          total_mib: 0,
          used_mib: 0,
        },
        resources: {
          gpu: {
            name: "NVIDIA H100 80GB HBM3",
            total_mib: 81920,
            used_mib: 0,
          },
        },
      };
      const fallbackCtx = resolveColabOneDigitalHumanGpuContext(
        zeroPrimaryRuntime,
        "L4",
      );
      expect(fallbackCtx.isLive).toBe(true);
      expect(fallbackCtx.vramMib).toBe(81920);
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          pair,
          zeroPrimaryRuntime,
          "L4",
        ),
      ).toBe("digital-human-longcat");
    });

    it("falls back to settingsGpu tier when runtime is stopped and ignores stale stopped GPU data", () => {
      const stoppedRuntime: RuntimeStatus = { state: "stopped" };
      const stoppedWithStaleT4: RuntimeStatus = {
        state: "stopped",
        gpu: {
          name: "Tesla T4",
          total_mib: 15360,
          used_mib: 0,
        },
      };

      for (const highTier of ["G4", "H100", "A100"] as const) {
        const ctx = resolveColabOneDigitalHumanGpuContext(
          stoppedRuntime,
          highTier,
        );
        expect(ctx.isLive, highTier).toBe(false);
        expect(ctx.vramMib, highTier).toBeGreaterThanOrEqual(49152);
        expect(ctx.shortLabel, highTier).toContain(highTier);
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            pair,
            stoppedRuntime,
            highTier,
          ),
          highTier,
        ).toBe("digital-human-longcat");
      }

      expect(
        resolveColabOneDigitalHumanGpuContext(stoppedWithStaleT4, "G4").isLive,
      ).toBe(false);
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          pair,
          stoppedWithStaleT4,
          "G4",
        ),
      ).toBe("digital-human-longcat");

      for (const lowTier of ["L4", "T4"] as const) {
        const ctx = resolveColabOneDigitalHumanGpuContext(
          stoppedRuntime,
          lowTier,
        );
        expect(ctx.isLive, lowTier).toBe(false);
        expect(ctx.vramMib, lowTier).toBeLessThan(49152);
        expect(ctx.shortLabel, lowTier).toContain(lowTier);
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            pair,
            stoppedRuntime,
            lowTier,
          ),
          lowTier,
        ).toBe("digital-human-infinitetalk");
      }
    });

    it("handles empty and single-workflow lists cleanly", () => {
      const stoppedRuntime: RuntimeStatus = { state: "stopped" };
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          [],
          stoppedRuntime,
          "G4",
        ),
      ).toBe("");
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          undefined,
          stoppedRuntime,
          "G4",
        ),
      ).toBe("");
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          [makeInfiniteTalkWorkflow()],
          stoppedRuntime,
          "G4",
        ),
      ).toBe("digital-human-infinitetalk");
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          [makeLongCatWorkflow()],
          stoppedRuntime,
          "T4",
        ),
      ).toBe("digital-human-longcat");
    });

    it("ignores stale GPU data in non-live states, rejects non-finite VRAM, trims settingsGpu, and handles starting state", () => {
      for (const nonLiveState of [
        "disconnected",
        "failed",
        "releasing",
      ] as const) {
        const staleRuntime: RuntimeStatus = {
          state: nonLiveState,
          gpu: {
            name: "Tesla T4",
            total_mib: 15360,
            used_mib: 0,
          },
          resources: {
            gpu: {
              name: "Tesla T4",
              total_mib: 15360,
              used_mib: 0,
            },
          },
        };
        const ctx = resolveColabOneDigitalHumanGpuContext(staleRuntime, "G4");
        expect(ctx.isLive, nonLiveState).toBe(false);
        expect(ctx.vramMib, nonLiveState).toBeGreaterThanOrEqual(49152);
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            pair,
            staleRuntime,
            "G4",
          ),
          nonLiveState,
        ).toBe("digital-human-longcat");
      }

      // Starting runtime with live GPU uses live telemetry; without GPU falls back to settingsGpu.
      const startingWithGpu: RuntimeStatus = {
        state: "starting",
        gpu: {
          name: "NVIDIA RTX PRO 6000 Blackwell",
          total_mib: 98304,
          used_mib: 0,
        },
      };
      expect(
        resolveColabOneDigitalHumanGpuContext(startingWithGpu, "T4"),
      ).toMatchObject({
        isLive: true,
        vramMib: 98304,
      });
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          pair,
          startingWithGpu,
          "T4",
        ),
      ).toBe("digital-human-longcat");

      const startingWithoutGpu: RuntimeStatus = { state: "starting" };
      expect(
        resolveColabOneDigitalHumanGpuContext(startingWithoutGpu, "T4"),
      ).toMatchObject({
        isLive: false,
      });
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          pair,
          startingWithoutGpu,
          "T4",
        ),
      ).toBe("digital-human-infinitetalk");

      // Non-finite or negative VRAM is rejected cleanly and falls back to settingsGpu.
      for (const badTotalMib of [Number.POSITIVE_INFINITY, -1024, Number.NaN]) {
        const badVramRuntime: RuntimeStatus = {
          state: "ready",
          gpu: {
            name: "Broken GPU",
            total_mib: badTotalMib,
            used_mib: 0,
          },
        };
        const ctx = resolveColabOneDigitalHumanGpuContext(badVramRuntime, "T4");
        expect(ctx.isLive, String(badTotalMib)).toBe(false);
        expect(ctx.vramMib, String(badTotalMib)).toBeLessThan(49152);
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            pair,
            badVramRuntime,
            "T4",
          ),
          String(badTotalMib),
        ).toBe("digital-human-infinitetalk");
      }

      // Whitespace-padded settingsGpu resolves accurately instead of falling back to G4.
      const paddedCtx = resolveColabOneDigitalHumanGpuContext(
        { state: "stopped" },
        "  T4  ",
      );
      expect(paddedCtx.isLive).toBe(false);
      expect(paddedCtx.vramMib).toBeLessThan(49152);
      expect(paddedCtx.shortLabel).toContain("T4");
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          pair,
          { state: "stopped" },
          "  T4  ",
        ),
      ).toBe("digital-human-infinitetalk");
    });

    it("recommends longcat_avatar_native on >=48 GB / 96 GB GPUs and infinitetalk on <48 GB GPUs", () => {
      const nativePair = [
        makeInfiniteTalkWorkflow(),
        makeLongCatNativeWorkflow(),
      ];
      const nativeReversedPair = [
        makeLongCatNativeWorkflow(),
        makeInfiniteTalkWorkflow(),
      ];
      // Also verify video_backend === "longcat_avatar_native" matching independently of workflow id
      const customIdNativePair = [
        makeInfiniteTalkWorkflow(),
        makeLongCatNativeWorkflow({ id: "flagship-native-16" }),
      ];

      const highVramRuntimes: Array<{ label: string; runtime: RuntimeStatus }> =
        [
          {
            label: "live G4 96 GB",
            runtime: {
              state: "ready",
              gpu: {
                name: "NVIDIA RTX PRO 6000 Blackwell",
                total_mib: 98304,
                used_mib: 512,
              },
            },
          },
          {
            label: "live H100 80 GB",
            runtime: {
              state: "ready",
              gpu: {
                name: "NVIDIA H100 80GB HBM3",
                total_mib: 81920,
                used_mib: 1024,
              },
            },
          },
          {
            label: "live exact 48 GB",
            runtime: {
              state: "ready",
              gpu: {
                name: "NVIDIA L40S",
                total_mib: 49152,
                used_mib: 0,
              },
            },
          },
        ];

      for (const tc of highVramRuntimes) {
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            nativePair,
            tc.runtime,
            "T4",
          ),
          tc.label,
        ).toBe("digital-human-longcat");
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            nativeReversedPair,
            tc.runtime,
            "T4",
          ),
          `${tc.label} (reversed)`,
        ).toBe("digital-human-longcat");
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            customIdNativePair,
            tc.runtime,
            "T4",
          ),
          `${tc.label} (custom id via video_backend)`,
        ).toBe("flagship-native-16");
      }

      for (const highTier of ["G4", "H100", "A100", "A100_80"] as const) {
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            nativePair,
            { state: "stopped" },
            highTier,
          ),
          `stopped ${highTier}`,
        ).toBe("digital-human-longcat");
      }

      const a10080Ctx = resolveColabOneDigitalHumanGpuContext(
        { state: "stopped" },
        "  a100_80  ",
      );
      expect(a10080Ctx.isLive).toBe(false);
      expect(a10080Ctx.vramMib).toBe(81920);
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          nativePair,
          { state: "stopped" },
          "  a100_80  ",
        ),
      ).toBe("digital-human-longcat");

      const a10040Ctx = resolveColabOneDigitalHumanGpuContext(
        { state: "stopped" },
        "  A100_40  ",
      );
      expect(a10040Ctx.isLive).toBe(false);
      expect(a10040Ctx.vramMib).toBe(40960);
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          nativePair,
          { state: "stopped" },
          "A100_40",
        ),
      ).toBe("digital-human-infinitetalk");
      expect(
        resolveColabOneRecommendedDigitalHumanWorkflowId(
          pair,
          { state: "stopped" },
          "A100_40",
        ),
      ).toBe("digital-human-infinitetalk");

      const lowVramRuntimes: Array<{ label: string; runtime: RuntimeStatus }> =
        [
          {
            label: "live 49151 MiB",
            runtime: {
              state: "ready",
              gpu: { name: "NVIDIA L40S", total_mib: 49151, used_mib: 0 },
            },
          },
          {
            label: "live A100 40 GB",
            runtime: {
              state: "ready",
              gpu: {
                name: "NVIDIA A100-SXM4-40GB",
                total_mib: 40960,
                used_mib: 0,
              },
            },
          },
          {
            label: "live L4 24 GB",
            runtime: {
              state: "ready",
              gpu: { name: "NVIDIA L4", total_mib: 24576, used_mib: 0 },
            },
          },
          {
            label: "live T4 15 GB",
            runtime: {
              state: "ready",
              gpu: { name: "Tesla T4", total_mib: 15360, used_mib: 0 },
            },
          },
        ];

      for (const tc of lowVramRuntimes) {
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            nativePair,
            tc.runtime,
            "G4",
          ),
          tc.label,
        ).toBe("digital-human-infinitetalk");
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            customIdNativePair,
            tc.runtime,
            "G4",
          ),
          `${tc.label} (custom id)`,
        ).toBe("digital-human-infinitetalk");
      }

      for (const lowTier of ["L4", "T4"] as const) {
        expect(
          resolveColabOneRecommendedDigitalHumanWorkflowId(
            nativePair,
            { state: "stopped" },
            lowTier,
          ),
          `stopped ${lowTier}`,
        ).toBe("digital-human-infinitetalk");
      }
    });
  });

  describe("preferred digital human engine persistence and initial selection", () => {
    const pair = [makeInfiniteTalkWorkflow(), makeLongCatWorkflow()];

    it("reads, writes, trims, clears, and handles storage failures under colabone.comfy.digital_human.engine", () => {
      expect(COLABONE_STORAGE_KEYS.comfyDigitalHumanEngine).toBe(
        "colabone.comfy.digital_human.engine",
      );

      const storage = makeMockStorage();
      expect(readColabOnePreferredDigitalHumanEngine(storage)).toBe("");

      writeColabOnePreferredDigitalHumanEngine(
        "digital-human-infinitetalk",
        storage,
      );
      expect(storage.getItem("colabone.comfy.digital_human.engine")).toBe(
        "digital-human-infinitetalk",
      );
      expect(readColabOnePreferredDigitalHumanEngine(storage)).toBe(
        "digital-human-infinitetalk",
      );

      writeColabOnePreferredDigitalHumanEngine(
        "  digital-human-longcat  ",
        storage,
      );
      expect(storage.getItem("colabone.comfy.digital_human.engine")).toBe(
        "digital-human-longcat",
      );
      expect(readColabOnePreferredDigitalHumanEngine(storage)).toBe(
        "digital-human-longcat",
      );

      // Clearing with "" or whitespace removes the key
      const removeSpy = vi.spyOn(storage, "removeItem");
      writeColabOnePreferredDigitalHumanEngine("", storage);
      expect(removeSpy).toHaveBeenCalledWith(
        "colabone.comfy.digital_human.engine",
      );
      expect(storage.getItem("colabone.comfy.digital_human.engine")).toBeNull();
      expect(readColabOnePreferredDigitalHumanEngine(storage)).toBe("");

      writeColabOnePreferredDigitalHumanEngine("digital-human-longcat", storage);
      writeColabOnePreferredDigitalHumanEngine("   ", storage);
      expect(storage.getItem("colabone.comfy.digital_human.engine")).toBeNull();

      // Trims pre-seeded whitespace-padded raw storage values on read
      storage.setItem(
        "colabone.comfy.digital_human.engine",
        "  digital-human-infinitetalk  ",
      );
      expect(readColabOnePreferredDigitalHumanEngine(storage)).toBe(
        "digital-human-infinitetalk",
      );

      // Handles throwing storage and null storage gracefully
      const throwingStorage = makeThrowingStorage();
      expect(() =>
        writeColabOnePreferredDigitalHumanEngine(
          "digital-human-longcat",
          throwingStorage,
        ),
      ).not.toThrow();
      expect(() =>
        writeColabOnePreferredDigitalHumanEngine("", throwingStorage),
      ).not.toThrow();
      expect(readColabOnePreferredDigitalHumanEngine(throwingStorage)).toBe("");
      expect(() =>
        writeColabOnePreferredDigitalHumanEngine("digital-human-longcat", null),
      ).not.toThrow();
      expect(readColabOnePreferredDigitalHumanEngine(null)).toBe("");
    });

    it("falls back to GPU auto-recommendation with fromUserPreference false when preference is empty or invalid", () => {
      const liveG4: RuntimeStatus = {
        state: "ready",
        gpu: {
          name: "NVIDIA RTX PRO 6000 Blackwell",
          total_mib: 98304,
          used_mib: 0,
        },
      };
      const liveT4: RuntimeStatus = {
        state: "ready",
        gpu: {
          name: "Tesla T4",
          total_mib: 15360,
          used_mib: 0,
        },
      };

      for (const missingPref of [undefined, null, "", "   ", "unknown-engine"]) {
        expect(
          resolveColabOneInitialDigitalHumanWorkflowId(
            pair,
            liveG4,
            "T4",
            missingPref,
          ),
        ).toEqual({
          workflowId: "digital-human-longcat",
          recommendedWorkflowId: "digital-human-longcat",
          fromUserPreference: false,
        });

        expect(
          resolveColabOneInitialDigitalHumanWorkflowId(
            pair,
            liveT4,
            "G4",
            missingPref,
          ),
        ).toEqual({
          workflowId: "digital-human-infinitetalk",
          recommendedWorkflowId: "digital-human-infinitetalk",
          fromUserPreference: false,
        });
      }

      expect(
        resolveColabOneInitialDigitalHumanWorkflowId(
          [],
          liveG4,
          "G4",
          "digital-human-longcat",
        ),
      ).toEqual({
        workflowId: "",
        recommendedWorkflowId: "",
        fromUserPreference: false,
      });

      // Custom digital human workflow IDs are never accepted as a built-in engine preference
      const customDh = makeInfiniteTalkWorkflow({
        source: "custom",
        id: "custom-digital-human",
      });
      expect(
        resolveColabOneInitialDigitalHumanWorkflowId(
          [...pair, customDh],
          liveG4,
          "G4",
          "custom-digital-human",
        ),
      ).toEqual({
        workflowId: "digital-human-longcat",
        recommendedWorkflowId: "digital-human-longcat",
        fromUserPreference: false,
      });
    });

    it("respects a valid user preference while still reporting accurate GPU recommendedWorkflowId", () => {
      const liveG4: RuntimeStatus = {
        state: "ready",
        gpu: {
          name: "NVIDIA RTX PRO 6000 Blackwell",
          total_mib: 98304,
          used_mib: 0,
        },
      };
      const liveT4: RuntimeStatus = {
        state: "ready",
        gpu: {
          name: "Tesla T4",
          total_mib: 15360,
          used_mib: 0,
        },
      };

      // User manually chose InfiniteTalk on G4 96 GB
      expect(
        resolveColabOneInitialDigitalHumanWorkflowId(
          pair,
          liveG4,
          "G4",
          "digital-human-infinitetalk",
        ),
      ).toEqual({
        workflowId: "digital-human-infinitetalk",
        recommendedWorkflowId: "digital-human-longcat",
        fromUserPreference: true,
      });

      // User manually chose LongCat on T4 15 GB
      expect(
        resolveColabOneInitialDigitalHumanWorkflowId(
          pair,
          liveT4,
          "T4",
          "digital-human-longcat",
        ),
      ).toEqual({
        workflowId: "digital-human-longcat",
        recommendedWorkflowId: "digital-human-infinitetalk",
        fromUserPreference: true,
      });

      // Trims whitespace around valid preference and works on stopped runtime
      expect(
        resolveColabOneInitialDigitalHumanWorkflowId(
          pair,
          { state: "stopped" },
          "L4",
          "  digital-human-longcat  ",
        ),
      ).toEqual({
        workflowId: "digital-human-longcat",
        recommendedWorkflowId: "digital-human-infinitetalk",
        fromUserPreference: true,
      });

      // Matches recommendation and still marks fromUserPreference true
      expect(
        resolveColabOneInitialDigitalHumanWorkflowId(
          pair,
          liveG4,
          "G4",
          "digital-human-longcat",
        ),
      ).toEqual({
        workflowId: "digital-human-longcat",
        recommendedWorkflowId: "digital-human-longcat",
        fromUserPreference: true,
      });
    });
  });

  describe("transferColabOneDigitalHumanFields", () => {
    const infiniteTalk = makeInfiniteTalkWorkflow();
    const longCat = makeLongCatWorkflow();

    it("carries shared fields from InfiniteTalk to LongCat and resets LongCat engine-specific parameters", () => {
      const currentValues: Record<string, string | number | boolean> = {
        "1.image": "presenter_front.png",
        "2.audio": "narration_track.wav",
        "pipeline.audio_source": "tts",
        "pipeline.script_text": "Welcome to today's product launch livestream.",
        "pipeline.tts_engine": "qwen3_tts",
        "pipeline.voice_preset": "lin_xiaoman",
        "pipeline.voice_ref_audio": "speaker_sample.wav",
        "pipeline.voice_ref_text": "Exact reference transcript sentence.",
        "pipeline.voice_prompt": "Bright and confident studio host voice",
        "pipeline.emotion_ref_audio": "excited_ref.wav",
        "pipeline.emotion_prompt": "Upbeat, warm, and natural",
        "pipeline.pause_seconds": 0.44,
        "pipeline.target_resolution": "720x1280",
        "5.positive_prompt": "Custom warm cinema lighting with soft bokeh",
        "15.seed": 987654,
        "pipeline.upscale_mode": "ai_2x",
        "pipeline.max_segment_seconds": 35,
        "15.steps": 12,
      };
      const snapshot = { ...currentValues };

      const transferred = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCat,
        currentValues,
        "zh-CN",
      );

      // Shared fields carried over verbatim
      expect(transferred["1.image"]).toBe("presenter_front.png");
      expect(transferred["2.audio"]).toBe("narration_track.wav");
      expect(transferred["pipeline.audio_source"]).toBe("tts");
      expect(transferred["pipeline.script_text"]).toBe(
        "Welcome to today's product launch livestream.",
      );
      expect(transferred["pipeline.tts_engine"]).toBe("qwen3_tts");
      expect(transferred["pipeline.voice_preset"]).toBe("lin_xiaoman");
      expect(transferred["pipeline.voice_ref_audio"]).toBe("speaker_sample.wav");
      expect(transferred["pipeline.voice_ref_text"]).toBe(
        "Exact reference transcript sentence.",
      );
      expect(transferred["pipeline.voice_prompt"]).toBe(
        "Bright and confident studio host voice",
      );
      expect(transferred["pipeline.emotion_ref_audio"]).toBe("excited_ref.wav");
      expect(transferred["pipeline.emotion_prompt"]).toBe(
        "Upbeat, warm, and natural",
      );
      expect(transferred["pipeline.pause_seconds"]).toBe(0.44);
      expect(transferred["pipeline.target_resolution"]).toBe("720x1280");
      expect(transferred["5.positive_prompt"]).toBe(
        "Custom warm cinema lighting with soft bokeh",
      );
      expect(transferred["15.seed"]).toBe(987654);
      expect(transferred["pipeline.upscale_mode"]).toBe("ai_2x");

      // Engine-specific keys reset to LongCat defaults and 15.steps omitted
      expect(transferred["pipeline.max_segment_seconds"]).toBe(3.7);
      expect(transferred["14.steps"]).toBe(8);
      expect(transferred["pipeline.render_quality"]).toBe("balanced_480p_ai2x");
      expect(transferred["pipeline.acceleration_profile"]).toBe("max");
      expect("15.steps" in transferred).toBe(false);

      // Does not mutate source input record
      expect(currentValues).toEqual(snapshot);
    });

    it("normalizes preset visual prompts to target locale while preserving custom prompts during transfer", () => {
      const fromCanonical = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCat,
        {
          "5.positive_prompt": COLABONE_CANONICAL_VISUAL_PROMPT,
          "pipeline.script_text": "hello",
        },
        "ja-JP",
      );
      expect(fromCanonical["5.positive_prompt"]).toBe(
        t("comfy.defaultVisualPrompt", undefined, "ja-JP"),
      );

      const fromOtherLocalePreset = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCat,
        {
          "5.positive_prompt": t(
            "comfy.defaultVisualPrompt",
            undefined,
            "zh-CN",
          ),
        },
        "en-US",
      );
      expect(fromOtherLocalePreset["5.positive_prompt"]).toBe(
        t("comfy.defaultVisualPrompt", undefined, "en-US"),
      );

      const legacyPrompt = Array.from(COLABONE_LEGACY_VISUAL_PROMPTS)[0];
      const fromLegacyPreset = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCat,
        {
          "5.positive_prompt": legacyPrompt,
        },
        "ko-KR",
      );
      expect(fromLegacyPreset["5.positive_prompt"]).toBe(
        t("comfy.defaultVisualPrompt", undefined, "ko-KR"),
      );
    });

    it("resets native_720p auto-synced lanczos upscale_mode back to ai_2x when switching from LongCat to InfiniteTalk", () => {
      const transferred = transferColabOneDigitalHumanFields(
        longCat,
        infiniteTalk,
        {
          "1.image": "studio_host.png",
          "2.audio": "voice.wav",
          "pipeline.audio_source": "tts",
          "pipeline.script_text": "LongCat native 720p narration script.",
          "pipeline.tts_engine": "voxcpm2",
          "pipeline.voice_preset": "chen_jingzhou",
          "pipeline.voice_ref_audio": "ref_male.wav",
          "pipeline.voice_ref_text": "Reference line",
          "pipeline.voice_prompt": "Deep calm baritone",
          "pipeline.emotion_prompt": "Serious documentary tone",
          "pipeline.pause_seconds": 0.36,
          "pipeline.target_resolution": "1920x1080",
          "5.positive_prompt": "Documentary broadcast look, crisp lighting",
          "15.seed": 202605,
          "pipeline.render_quality": "native_720p",
          "pipeline.acceleration_profile": "balanced",
          "pipeline.upscale_mode": "lanczos",
          "pipeline.max_segment_seconds": 5.5,
          "14.steps": 12,
        },
        "en-US",
      );

      // Common fields carried over
      expect(transferred["1.image"]).toBe("studio_host.png");
      expect(transferred["2.audio"]).toBe("voice.wav");
      expect(transferred["pipeline.audio_source"]).toBe("tts");
      expect(transferred["pipeline.script_text"]).toBe(
        "LongCat native 720p narration script.",
      );
      expect(transferred["pipeline.tts_engine"]).toBe("voxcpm2");
      expect(transferred["pipeline.voice_preset"]).toBe("chen_jingzhou");
      expect(transferred["pipeline.voice_ref_audio"]).toBe("ref_male.wav");
      expect(transferred["pipeline.voice_ref_text"]).toBe("Reference line");
      expect(transferred["pipeline.voice_prompt"]).toBe("Deep calm baritone");
      expect(transferred["pipeline.emotion_prompt"]).toBe(
        "Serious documentary tone",
      );
      expect(transferred["pipeline.pause_seconds"]).toBe(0.36);
      expect(transferred["pipeline.target_resolution"]).toBe("1920x1080");
      expect(transferred["5.positive_prompt"]).toBe(
        "Documentary broadcast look, crisp lighting",
      );
      expect(transferred["15.seed"]).toBe(202605);

      // Engine-specific parameters reset to InfiniteTalk defaults and auto-synced lanczos cleared
      expect(transferred["pipeline.max_segment_seconds"]).toBe(20);
      expect(transferred["15.steps"]).toBe(8);
      expect(transferred["pipeline.upscale_mode"]).toBe("ai_2x");
      expect("14.steps" in transferred).toBe(false);
      expect("pipeline.render_quality" in transferred).toBe(false);
      expect("pipeline.acceleration_profile" in transferred).toBe(false);
    });

    it("preserves explicit upscale_mode choices when switching from LongCat balanced_480p_ai2x to InfiniteTalk", () => {
      const withNone = transferColabOneDigitalHumanFields(
        longCat,
        infiniteTalk,
        {
          "1.image": "host.png",
          "pipeline.audio_source": "tts",
          "pipeline.script_text": "Explicit none upscale check.",
          "pipeline.render_quality": "balanced_480p_ai2x",
          "pipeline.upscale_mode": "none",
          "pipeline.max_segment_seconds": 4.2,
          "14.steps": 10,
          "15.seed": 77,
        },
        "zh-CN",
      );

      expect(withNone["pipeline.upscale_mode"]).toBe("none");
      expect(withNone["pipeline.max_segment_seconds"]).toBe(20);
      expect(withNone["15.steps"]).toBe(8);
      expect(withNone["15.seed"]).toBe(77);

      const withExplicitLanczos = transferColabOneDigitalHumanFields(
        longCat,
        infiniteTalk,
        {
          "pipeline.render_quality": "balanced_480p_ai2x",
          "pipeline.upscale_mode": "lanczos",
        },
        "zh-CN",
      );
      expect(withExplicitLanczos["pipeline.upscale_mode"]).toBe("lanczos");
    });

    it("validates select options, clamps numeric bounds, and handles undefined workflows safely", () => {
      const sanitized = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCat,
        {
          "pipeline.target_resolution": "9999x9999",
          "pipeline.pause_seconds": 9.5,
          "15.seed": -12,
        },
        "zh-CN",
      );
      expect(sanitized["pipeline.target_resolution"]).toBe("1280x720");
      expect(sanitized["pipeline.pause_seconds"]).toBe(1.5);
      expect(sanitized["15.seed"]).toBe(0);

      expect(
        transferColabOneDigitalHumanFields(
          infiniteTalk,
          undefined,
          { "1.image": "host.png" },
          "zh-CN",
        ),
      ).toEqual({});
      expect(
        transferColabOneDigitalHumanFields(undefined, longCat, {}, "zh-CN"),
      ).toEqual(defaultFieldValues(longCat.fields, "zh-CN"));
    });

    it("preserves dynamic Qwen3-TTS official speaker presets and uploaded audio mode across engine switches", () => {
      const toLongCat = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCat,
        {
          "pipeline.audio_source": "audio",
          "2.audio": "pre_recorded_host.wav",
          "pipeline.tts_engine": "qwen3_tts",
          "pipeline.voice_preset": "qwen_vivian",
        },
        "zh-CN",
      );
      expect(toLongCat["pipeline.audio_source"]).toBe("audio");
      expect(toLongCat["2.audio"]).toBe("pre_recorded_host.wav");
      expect(toLongCat["pipeline.tts_engine"]).toBe("qwen3_tts");
      expect(toLongCat["pipeline.voice_preset"]).toBe("qwen_vivian");

      const backToInfiniteTalk = transferColabOneDigitalHumanFields(
        longCat,
        infiniteTalk,
        {
          "pipeline.audio_source": "audio",
          "2.audio": "pre_recorded_host.wav",
          "pipeline.tts_engine": "qwen3_tts",
          "pipeline.voice_preset": "qwen_vivian",
        },
        "zh-CN",
      );
      expect(backToInfiniteTalk["pipeline.audio_source"]).toBe("audio");
      expect(backToInfiniteTalk["2.audio"]).toBe("pre_recorded_host.wav");
      expect(backToInfiniteTalk["pipeline.tts_engine"]).toBe("qwen3_tts");
      expect(backToInfiniteTalk["pipeline.voice_preset"]).toBe("qwen_vivian");
    });

    it("preserves InfiniteTalk upscale_mode with stale native_720p and validates booleans, empty numbers, negative zero, mismatched field types, and non-string media", () => {
      // InfiniteTalk does not own pipeline.render_quality, so stale native_720p must not discard explicit lanczos/none.
      const keepLanczos = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCat,
        {
          "pipeline.render_quality": "native_720p",
          "pipeline.upscale_mode": "lanczos",
        },
        "zh-CN",
      );
      expect(keepLanczos["pipeline.upscale_mode"]).toBe("lanczos");
      expect(keepLanczos["pipeline.render_quality"]).toBe("balanced_480p_ai2x");

      const keepNone = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCat,
        {
          "pipeline.upscale_mode": "none",
        },
        "zh-CN",
      );
      expect(keepNone["pipeline.upscale_mode"]).toBe("none");

      // Non-string image/audio values fall back to target defaults.
      const badMedia = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCat,
        {
          "1.image": true as unknown as string,
          "2.audio": 42 as unknown as string,
          "pipeline.pause_seconds": "",
          "15.seed": -0.1,
        },
        "zh-CN",
      );
      expect(badMedia["1.image"]).toBe("");
      expect(badMedia["2.audio"]).toBe("");
      expect(badMedia["pipeline.pause_seconds"]).toBe("");
      expect(Object.is(badMedia["15.seed"], 0)).toBe(true);

      // Synthetic workflow fields covering boolean parsing, -0 normalization, and mismatched field types.
      const customSource = makeWorkflow({
        id: "synthetic-src",
        source: "builtin",
        pipeline: { kind: "digital_human", video_backend: "infinitetalk" },
        fields: [
          { key: "flag.bool", node_id: "flag", input_name: "bool", label: "Bool", field_type: "boolean", default: false },
          { key: "flag.str_true", node_id: "flag", input_name: "str_true", label: "StrTrue", field_type: "boolean", default: false },
          { key: "flag.str_one", node_id: "flag", input_name: "str_one", label: "StrOne", field_type: "boolean", default: false },
          { key: "flag.str_false", node_id: "flag", input_name: "str_false", label: "StrFalse", field_type: "boolean", default: true },
          { key: "flag.str_zero", node_id: "flag", input_name: "str_zero", label: "StrZero", field_type: "boolean", default: true },
          { key: "flag.invalid", node_id: "flag", input_name: "invalid", label: "Invalid", field_type: "boolean", default: true },
          { key: "num.negzero", node_id: "num", input_name: "negzero", label: "NegZero", field_type: "number", default: 1 },
          { key: "mismatch.field", node_id: "mismatch", input_name: "field", label: "Mismatch", field_type: "text", default: "a" },
        ],
      });
      const customTarget = makeWorkflow({
        id: "synthetic-dst",
        source: "builtin",
        pipeline: { kind: "digital_human", video_backend: "longcat_avatar" },
        fields: [
          { key: "flag.bool", node_id: "flag", input_name: "bool", label: "Bool", field_type: "boolean", default: false },
          { key: "flag.str_true", node_id: "flag", input_name: "str_true", label: "StrTrue", field_type: "boolean", default: false },
          { key: "flag.str_one", node_id: "flag", input_name: "str_one", label: "StrOne", field_type: "boolean", default: false },
          { key: "flag.str_false", node_id: "flag", input_name: "str_false", label: "StrFalse", field_type: "boolean", default: true },
          { key: "flag.str_zero", node_id: "flag", input_name: "str_zero", label: "StrZero", field_type: "boolean", default: true },
          { key: "flag.invalid", node_id: "flag", input_name: "invalid", label: "Invalid", field_type: "boolean", default: true },
          { key: "num.negzero", node_id: "num", input_name: "negzero", label: "NegZero", field_type: "number", default: 1 },
          { key: "mismatch.field", node_id: "mismatch", input_name: "field", label: "Mismatch", field_type: "integer", default: 9 },
        ],
      });

      const edgeTransferred = transferColabOneDigitalHumanFields(
        customSource,
        customTarget,
        {
          "flag.bool": true,
          "flag.str_true": "true",
          "flag.str_one": "1",
          "flag.str_false": "false",
          "flag.str_zero": "0",
          "flag.invalid": "maybe",
          "num.negzero": -0,
          "mismatch.field": "42",
        },
        "zh-CN",
      );
      expect(edgeTransferred["flag.bool"]).toBe(true);
      expect(edgeTransferred["flag.str_true"]).toBe(true);
      expect(edgeTransferred["flag.str_one"]).toBe(true);
      expect(edgeTransferred["flag.str_false"]).toBe(false);
      expect(edgeTransferred["flag.str_zero"]).toBe(false);
      expect(edgeTransferred["flag.invalid"]).toBe(true);
      expect(Object.is(edgeTransferred["num.negzero"], 0)).toBe(true);
      expect(edgeTransferred["mismatch.field"]).toBe(9);
    });

    it("transfers shared media/TTS/resolution fields between InfiniteTalk and LongCat 1.6 native while resetting incompatible upscale and engine-specific keys", () => {
      const longCatNative = makeLongCatNativeWorkflow();

      // 1. InfiniteTalk to LongCat 1.6 native: shared fields preserved, ai_2x/lanczos reset to auto, 14 native keys reset to defaults
      const toNative = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCatNative,
        {
          "1.image": "presenter_halfbody.png",
          "2.audio": "studio_audio.wav",
          "pipeline.audio_source": "tts",
          "pipeline.script_text": "96GB 电影级旗舰引擎切换验证口播稿。",
          "pipeline.tts_engine": "qwen3_tts",
          "pipeline.voice_preset": "qwen_vivian",
          "pipeline.voice_ref_audio": "clone_sample.wav",
          "pipeline.voice_ref_text": "参考音频对应文字",
          "pipeline.voice_prompt": "Warm articulate broadcast voice",
          "pipeline.emotion_ref_audio": "emotion_sample.wav",
          "pipeline.emotion_prompt": "Confident and expressive",
          "pipeline.pause_seconds": 0.42,
          "pipeline.target_resolution": "1080x1920",
          "pipeline.upscale_mode": "ai_2x",
          "pipeline.max_segment_seconds": 35,
          "5.positive_prompt": "Custom InfiniteTalk visual prompt",
          "15.steps": 12,
          "15.seed": 777,
          // Stale native keys present in record must STILL reset to target defaults
          "pipeline.quality_preset": "longform_fast",
          "pipeline.framing_style": "full_body_dynamic",
          "pipeline.sampling_mode": "distill_lora_mix",
          "pipeline.seed": 9999,
          "pipeline.positive_prompt": "Stale prompt",
        },
        "zh-CN",
      );

      // Shared fields preserved
      expect(toNative["1.image"]).toBe("presenter_halfbody.png");
      expect(toNative["2.audio"]).toBe("studio_audio.wav");
      expect(toNative["pipeline.audio_source"]).toBe("tts");
      expect(toNative["pipeline.script_text"]).toBe(
        "96GB 电影级旗舰引擎切换验证口播稿。",
      );
      expect(toNative["pipeline.tts_engine"]).toBe("qwen3_tts");
      expect(toNative["pipeline.voice_preset"]).toBe("qwen_vivian");
      expect(toNative["pipeline.voice_ref_audio"]).toBe("clone_sample.wav");
      expect(toNative["pipeline.voice_ref_text"]).toBe("参考音频对应文字");
      expect(toNative["pipeline.voice_prompt"]).toBe(
        "Warm articulate broadcast voice",
      );
      expect(toNative["pipeline.emotion_ref_audio"]).toBe("emotion_sample.wav");
      expect(toNative["pipeline.emotion_prompt"]).toBe(
        "Confident and expressive",
      );
      expect(toNative["pipeline.pause_seconds"]).toBe(0.42);
      expect(toNative["pipeline.target_resolution"]).toBe("1080x1920");

      // Incompatible InfiniteTalk upscale_mode ("ai_2x") resets to LongCat 1.6 default ("auto")
      expect(toNative["pipeline.upscale_mode"]).toBe("auto");

      // All 14 LongCat 1.6 native engine-specific keys reset to defaults
      expect(toNative["pipeline.quality_preset"]).toBe("cinema_flagship");
      expect(toNative["pipeline.framing_style"]).toBe("half_body_gestures");
      expect(toNative["pipeline.sampling_mode"]).toBe("auto");
      expect(toNative["pipeline.audio_guidance_scale"]).toBe(4.0);
      expect(toNative["pipeline.text_guidance_scale"]).toBe(4.0);
      expect(toNative["pipeline.num_inference_steps"]).toBe(50);
      expect(toNative["pipeline.mix_steps"]).toBe(4);
      expect(toNative["pipeline.mix_alpha"]).toBe(1.0);
      expect(toNative["pipeline.vocal_separation"]).toBe("auto");
      expect(toNative["pipeline.ref_img_index"]).toBe(10);
      expect(toNative["pipeline.seed"]).toBe(42);
      expect(toNative["pipeline.crf"]).toBe(16);
      expect(toNative["pipeline.positive_prompt"]).toBe("");
      expect(toNative["pipeline.negative_prompt"]).toBe("");

      // InfiniteTalk-only keys omitted
      expect("pipeline.max_segment_seconds" in toNative).toBe(false);
      expect("5.positive_prompt" in toNative).toBe(false);
      expect("15.steps" in toNative).toBe(false);
      expect("15.seed" in toNative).toBe(false);

      // Compatible upscale_mode "none" IS preserved when switching InfiniteTalk to LongCat 1.6 native
      const toNativeKeepNone = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCatNative,
        {
          "pipeline.upscale_mode": "none",
          "pipeline.target_resolution": "720x1280",
        },
        "zh-CN",
      );
      expect(toNativeKeepNone["pipeline.upscale_mode"]).toBe("none");
      expect(toNativeKeepNone["pipeline.target_resolution"]).toBe("720x1280");

      // 2. LongCat 1.6 native to InfiniteTalk: shared fields preserved, incompatible 2K & upscale reset cleanly
      const backToInfiniteTalk = transferColabOneDigitalHumanFields(
        longCatNative,
        infiniteTalk,
        {
          "1.image": "presenter_halfbody.png",
          "2.audio": "studio_audio.wav",
          "pipeline.audio_source": "audio",
          "pipeline.script_text": "Retained script text",
          "pipeline.tts_engine": "fish_drama3",
          "pipeline.voice_preset": "su_wanqing",
          "pipeline.voice_ref_audio": "clone_sample.wav",
          "pipeline.voice_ref_text": "Transcript",
          "pipeline.voice_prompt": "Gentle host",
          "pipeline.emotion_prompt": "Warm",
          "pipeline.pause_seconds": 0.36,
          "pipeline.target_resolution": "2560x1440",
          "pipeline.quality_preset": "balanced_hd",
          "pipeline.framing_style": "closeup_presenter",
          "pipeline.upscale_mode": "seedvr2_7b_sharp",
          "pipeline.sampling_mode": "distill_lora_mix",
          "pipeline.audio_guidance_scale": 5.2,
          "pipeline.text_guidance_scale": 3.5,
          "pipeline.num_inference_steps": 30,
          "pipeline.mix_steps": 3,
          "pipeline.mix_alpha": 0.8,
          "pipeline.vocal_separation": "always",
          "pipeline.ref_img_index": 15,
          "pipeline.seed": 8888,
          "pipeline.crf": 14,
          "pipeline.positive_prompt": "Custom native prompt",
          "pipeline.negative_prompt": "blurry teeth",
        },
        "en-US",
      );

      // Shared fields preserved
      expect(backToInfiniteTalk["1.image"]).toBe("presenter_halfbody.png");
      expect(backToInfiniteTalk["2.audio"]).toBe("studio_audio.wav");
      expect(backToInfiniteTalk["pipeline.audio_source"]).toBe("audio");
      expect(backToInfiniteTalk["pipeline.script_text"]).toBe(
        "Retained script text",
      );
      expect(backToInfiniteTalk["pipeline.tts_engine"]).toBe("fish_drama3");
      expect(backToInfiniteTalk["pipeline.voice_preset"]).toBe("su_wanqing");
      expect(backToInfiniteTalk["pipeline.voice_ref_audio"]).toBe(
        "clone_sample.wav",
      );
      expect(backToInfiniteTalk["pipeline.voice_ref_text"]).toBe("Transcript");
      expect(backToInfiniteTalk["pipeline.voice_prompt"]).toBe("Gentle host");
      expect(backToInfiniteTalk["pipeline.emotion_prompt"]).toBe("Warm");
      expect(backToInfiniteTalk["pipeline.pause_seconds"]).toBe(0.36);

      // Landscape 2K resolution steps down preserving orientation to 1920x1080; SeedVR2 upscale resets to InfiniteTalk default
      expect(backToInfiniteTalk["pipeline.target_resolution"]).toBe("1920x1080");
      expect(backToInfiniteTalk["pipeline.upscale_mode"]).toBe("ai_2x");

      // InfiniteTalk engine-specific & localized fields set to defaults
      expect(backToInfiniteTalk["pipeline.max_segment_seconds"]).toBe(20);
      expect(backToInfiniteTalk["15.steps"]).toBe(8);
      expect(backToInfiniteTalk["15.seed"]).toBe(42);
      expect(backToInfiniteTalk["5.positive_prompt"]).toBe(
        t("comfy.defaultVisualPrompt", undefined, "en-US"),
      );

      // All LongCat 1.6 native-only keys omitted from InfiniteTalk form state
      for (const nativeOnlyKey of [
        "pipeline.quality_preset",
        "pipeline.framing_style",
        "pipeline.sampling_mode",
        "pipeline.audio_guidance_scale",
        "pipeline.text_guidance_scale",
        "pipeline.num_inference_steps",
        "pipeline.mix_steps",
        "pipeline.mix_alpha",
        "pipeline.vocal_separation",
        "pipeline.ref_img_index",
        "pipeline.seed",
        "pipeline.crf",
        "pipeline.positive_prompt",
        "pipeline.negative_prompt",
      ]) {
        expect(nativeOnlyKey in backToInfiniteTalk, nativeOnlyKey).toBe(false);
      }

      // 3. Untouched source default target_resolution adopts target default in both directions
      const untouchedLongCatToInfiniteTalk = transferColabOneDigitalHumanFields(
        longCatNative,
        infiniteTalk,
        {
          "pipeline.target_resolution": "1920x1080",
          "pipeline.upscale_mode": "none",
        },
        "zh-CN",
      );
      expect(untouchedLongCatToInfiniteTalk["pipeline.target_resolution"]).toBe(
        "1280x720",
      );
      expect(untouchedLongCatToInfiniteTalk["pipeline.upscale_mode"]).toBe(
        "none",
      );

      const untouchedInfiniteTalkToLongCat = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCatNative,
        {
          "pipeline.target_resolution": "1280x720",
        },
        "zh-CN",
      );
      expect(untouchedInfiniteTalkToLongCat["pipeline.target_resolution"]).toBe(
        "1920x1080",
      );

      // 4. Portrait 2K (1440x2560) steps down preserving portrait orientation to 1080x1920
      const portrait2kToInfiniteTalk = transferColabOneDigitalHumanFields(
        longCatNative,
        infiniteTalk,
        {
          "pipeline.target_resolution": "1440x2560",
        },
        "zh-CN",
      );
      expect(portrait2kToInfiniteTalk["pipeline.target_resolution"]).toBe(
        "1080x1920",
      );

      // When a target only supports 720p portrait/landscape, 2K steps down to matching 720p orientation
      const hdOnlyTarget = makeInfiniteTalkWorkflow({
        fields: infiniteTalk.fields.map((f) =>
          f.key === "pipeline.target_resolution"
            ? { ...f, options: ["1280x720", "720x1280"] }
            : f,
        ),
      });
      expect(
        transferColabOneDigitalHumanFields(
          longCatNative,
          hdOnlyTarget,
          { "pipeline.target_resolution": "1440x2560" },
          "zh-CN",
        )["pipeline.target_resolution"],
      ).toBe("720x1280");
      expect(
        transferColabOneDigitalHumanFields(
          longCatNative,
          hdOnlyTarget,
          { "pipeline.target_resolution": "2560x1440" },
          "zh-CN",
        )["pipeline.target_resolution"],
      ).toBe("1280x720");

      // 5. User-customized shared target_resolution and voice_preset validation work cleanly
      const customizedBackToInfiniteTalk = transferColabOneDigitalHumanFields(
        longCatNative,
        infiniteTalk,
        {
          "pipeline.target_resolution": "1080x1920",
          "pipeline.voice_preset": "../bad_preset!",
        },
        "zh-CN",
      );
      expect(customizedBackToInfiniteTalk["pipeline.target_resolution"]).toBe(
        "1080x1920",
      );
      expect(customizedBackToInfiniteTalk["pipeline.voice_preset"]).toBe(
        "shen_zhiyi",
      );

      const customizedInfiniteTalkToLongCat = transferColabOneDigitalHumanFields(
        infiniteTalk,
        longCatNative,
        {
          "pipeline.target_resolution": "1920x1080",
          "pipeline.voice_preset": "qwen_vivian",
        },
        "zh-CN",
      );
      expect(customizedInfiniteTalkToLongCat["pipeline.target_resolution"]).toBe(
        "1920x1080",
      );
      expect(customizedInfiniteTalkToLongCat["pipeline.voice_preset"]).toBe(
        "qwen_vivian",
      );

      // Corrupted non-string audio_source falls back cleanly to "tts"
      expect(
        resolveColabOneAudioSource({
          "pipeline.audio_source": true,
        }),
      ).toBe("tts");
      expect(
        resolveColabOneAudioSource({
          "pipeline.audio_source": 123,
        }),
      ).toBe("tts");
    });

    it("handles missing and uppercase AUDIO/TTS in visibility and override building", () => {
      const audioSourceField = makeField({
        key: "pipeline.audio_source",
        field_type: "select",
        default: "tts",
        options: ["tts", "audio"],
      });
      const scriptField = makeField({
        key: "pipeline.script_text",
        field_type: "textarea",
        default: "",
        visible_when: { "pipeline.audio_source": "tts" },
      });
      const audioUploadField = makeField({
        key: "2.audio",
        field_type: "audio",
        default: "",
        visible_when: { "pipeline.audio_source": "audio" },
      });
      const wf = makeInfiniteTalkWorkflow({
        fields: [audioSourceField, scriptField, audioUploadField],
      });

      // Missing audio_source resolves to "tts"
      expect(isColabOneWorkflowFieldVisible(wf, scriptField, {})).toBe(true);
      expect(isColabOneWorkflowFieldVisible(wf, audioUploadField, {})).toBe(
        false,
      );

      // Uppercase / whitespace-padded "TTS" and "AUDIO"
      expect(
        isColabOneWorkflowFieldVisible(wf, scriptField, {
          "pipeline.audio_source": " TTS ",
        }),
      ).toBe(true);
      expect(
        isColabOneWorkflowFieldVisible(wf, audioUploadField, {
          "pipeline.audio_source": " TTS ",
        }),
      ).toBe(false);
      expect(
        isColabOneWorkflowFieldVisible(wf, scriptField, {
          "pipeline.audio_source": " AUDIO ",
        }),
      ).toBe(false);
      expect(
        isColabOneWorkflowFieldVisible(wf, audioUploadField, {
          "pipeline.audio_source": " AUDIO ",
        }),
      ).toBe(true);

      // Override building canonicalizes uppercase "AUDIO" / "TTS" and filters hidden fields consistently
      const audioPayload = buildColabOneWorkflowOverrides(
        wf,
        {
          "pipeline.audio_source": " AUDIO ",
          "pipeline.script_text": "ignored in audio mode",
          "2.audio": "voice.wav",
        },
        undefined,
        { uploadedInputIds: { "2.audio": "asset-audio-1" } },
      );
      expect(audioPayload.overrides["pipeline.audio_source"]).toBe("audio");
      expect(audioPayload.inputs["2.audio"]).toBe("asset-audio-1");
      expect("pipeline.script_text" in audioPayload.overrides).toBe(false);

      const ttsPayload = buildColabOneWorkflowOverrides(
        wf,
        {
          "pipeline.audio_source": "TTS",
          "pipeline.script_text": "hello world",
          "2.audio": "ignored.wav",
        },
        undefined,
        { uploadedInputIds: { "2.audio": "asset-ignored" } },
      );
      expect(ttsPayload.overrides["pipeline.audio_source"]).toBe("tts");
      expect(ttsPayload.overrides["pipeline.script_text"]).toBe("hello world");
      expect("2.audio" in ttsPayload.inputs).toBe(false);
      expect("2.audio" in ttsPayload.overrides).toBe(false);

      // Missing pipeline.audio_source keeps TTS-visible fields included
      const missingPayload = buildColabOneWorkflowOverrides(
        wf,
        {
          "pipeline.script_text": "default tts script",
        },
        undefined,
        {},
      );
      expect(missingPayload.overrides["pipeline.audio_source"]).toBe("tts");
      expect(missingPayload.overrides["pipeline.script_text"]).toBe(
        "default tts script",
      );
    });

    it("canonicalizes case-insensitive CUSTOM voice preset across override, validation, engine lookup, and transfer helpers", () => {
      const indexTtsEngine: ComfyTtsEngineSpec = {
        id: "indextts2",
        name: "IndexTTS-2.5",
        description: "",
        repo: "IndexTeam/IndexTTS-2.5",
        revision: "rev-indextts25",
        license: "Non-Commercial",
        commercial_ok: false,
        license_notice: "Non-commercial only",
        min_vram_mib: 8192,
        min_disk_mib: 15360,
        sample_rate: 22050,
        max_segment_chars: 180,
        supports_voice_clone: true,
        supports_voice_design: false,
        supports_emotion_ref: true,
        supports_inline_tags: true,
        supports_custom_speakers: false,
        voice_presets: [
          {
            id: "shen_zhiyi",
            name: "沈知意",
            gender: "female",
            style: "知性主播",
            description: "",
            prompt: "",
            preview_text: "",
            speaker_id: "",
            seed: 1,
          },
        ],
      };

      const presetField = makeField({
        key: "pipeline.voice_preset",
        field_type: "select",
        default: "shen_zhiyi",
        options: ["shen_zhiyi", "custom"],
      });
      const customDefaultField = makeField({
        key: "pipeline.voice_preset",
        field_type: "select",
        default: " CUSTOM ",
        options: ["custom"],
      });
      const presetWf = makeInfiniteTalkWorkflow({ fields: [presetField] });

      const { overrides } = buildColabOneWorkflowOverrides(presetWf, {
        "pipeline.voice_preset": " CUSTOM ",
      });
      expect(overrides["pipeline.voice_preset"]).toBe("custom");

      expect(
        isColabOneVoicePresetSupportedByEngine(
          "CUSTOM",
          presetField,
          indexTtsEngine,
        ),
      ).toBe(true);
      expect(
        resolveColabOneSelectedVoicePreset(" Custom ", indexTtsEngine),
      ).toBeUndefined();
      expect(
        resolveColabOneFallbackVoicePreset(customDefaultField, undefined),
      ).toBe("custom");

      const wf = makeInfiniteTalkWorkflow();
      expect(
        isColabOneVoiceRefRequired(wf, {
          "pipeline.audio_source": "tts",
          "pipeline.tts_engine": "indextts2",
          "pipeline.voice_preset": "CUSTOM",
        }),
      ).toBe(true);

      const transferred = transferColabOneDigitalHumanFields(
        wf,
        makeLongCatNativeWorkflow(),
        { "pipeline.voice_preset": " CuStOm " },
        "zh-CN",
      );
      expect(transferred["pipeline.voice_preset"]).toBe("custom");
    });

    it("validates text and textarea types in buildColabOneWorkflowOverrides cleanly", () => {
      const promptField = makeField({
        key: "5.positive_prompt",
        field_type: "textarea",
        default: "default studio prompt",
      });
      const tagField = makeField({
        key: "pipeline.emotion_prompt",
        field_type: "text",
        default: "calm",
      });
      const textWf = makeInfiniteTalkWorkflow({
        fields: [promptField, tagField],
      });

      // Corrupted object, boolean, NaN, and Infinity fall back to field.default
      const corrupted = buildColabOneWorkflowOverrides(textWf, {
        "5.positive_prompt": { bad: true } as unknown as string,
        "pipeline.emotion_prompt": true,
      });
      expect(corrupted.overrides["5.positive_prompt"]).toBe(
        "default studio prompt",
      );
      expect(corrupted.overrides["pipeline.emotion_prompt"]).toBe("calm");

      const nonFinite = buildColabOneWorkflowOverrides(textWf, {
        "5.positive_prompt": Number.NaN,
        "pipeline.emotion_prompt": Number.POSITIVE_INFINITY,
      });
      expect(nonFinite.overrides["5.positive_prompt"]).toBe(
        "default studio prompt",
      );
      expect(nonFinite.overrides["pipeline.emotion_prompt"]).toBe("calm");

      // Omitted fields preserve non-empty field.default
      const omitted = buildColabOneWorkflowOverrides(textWf, {});
      expect(omitted.overrides["5.positive_prompt"]).toBe(
        "default studio prompt",
      );
      expect(omitted.overrides["pipeline.emotion_prompt"]).toBe("calm");

      // Explicit empty string stays "" instead of reverting to default
      const explicitEmpty = buildColabOneWorkflowOverrides(textWf, {
        "5.positive_prompt": "",
        "pipeline.emotion_prompt": "   ",
      });
      expect(explicitEmpty.overrides["5.positive_prompt"]).toBe("");
      expect(explicitEmpty.overrides["pipeline.emotion_prompt"]).toBe("");

      // Finite numbers stringify with -0 normalized to "0"
      const numericText = buildColabOneWorkflowOverrides(textWf, {
        "5.positive_prompt": 42,
        "pipeline.emotion_prompt": -0,
      });
      expect(numericText.overrides["5.positive_prompt"]).toBe("42");
      expect(numericText.overrides["pipeline.emotion_prompt"]).toBe("0");
    });

    it("roundtrips shipped catalog native LongCat 1.6 to InfiniteTalk and back cleanly", () => {
      const templates = comfyCatalog.templates as unknown as WorkflowTemplate[];
      const shippedLongCat = templates.find(
        (w) => w.id === "digital-human-longcat",
      );
      const shippedInfiniteTalk = templates.find(
        (w) => w.id === "digital-human-infinitetalk",
      );
      expect(shippedLongCat).toBeDefined();
      expect(shippedInfiniteTalk).toBeDefined();
      expect(shippedLongCat!.pipeline?.video_backend).toBe(
        "longcat_avatar_native",
      );

      // Start from shipped LongCat 1.6 defaults with audio_source set to "audio"
      const longCatStart: Record<string, string | number | boolean> = {
        ...defaultFieldValues(shippedLongCat!.fields, "zh-CN"),
        "pipeline.audio_source": "audio",
      };
      expect(longCatStart["pipeline.upscale_mode"]).toBe("auto");
      expect(longCatStart["pipeline.target_resolution"]).toBe("1920x1080");

      // Transfer shipped LongCat 1.6 to shipped InfiniteTalk
      const toInfiniteTalk = transferColabOneDigitalHumanFields(
        shippedLongCat,
        shippedInfiniteTalk,
        longCatStart,
        "zh-CN",
      );
      expect(toInfiniteTalk["pipeline.upscale_mode"]).toBe("ai_2x");
      expect(toInfiniteTalk["pipeline.target_resolution"]).toBe("1280x720");
      expect(toInfiniteTalk["pipeline.tts_engine"]).toBe("moss_tts_v1_5");
      expect(toInfiniteTalk["pipeline.audio_source"]).toBe("audio");

      // Transfer back from shipped InfiniteTalk to shipped LongCat 1.6
      const backToLongCat = transferColabOneDigitalHumanFields(
        shippedInfiniteTalk,
        shippedLongCat,
        toInfiniteTalk,
        "zh-CN",
      );
      expect(backToLongCat["pipeline.upscale_mode"]).toBe("auto");
      expect(backToLongCat["pipeline.target_resolution"]).toBe("1920x1080");
      expect(backToLongCat["pipeline.tts_engine"]).toBe("fish_drama3");
      expect(backToLongCat["pipeline.audio_source"]).toBe("audio");

      // User-customized TTS engine is still preserved across the roundtrip
      const customTtsToInfiniteTalk = transferColabOneDigitalHumanFields(
        shippedLongCat,
        shippedInfiniteTalk,
        { ...longCatStart, "pipeline.tts_engine": "qwen3_tts" },
        "zh-CN",
      );
      expect(customTtsToInfiniteTalk["pipeline.tts_engine"]).toBe("qwen3_tts");
    });
  });
});
