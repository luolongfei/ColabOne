import { afterEach, describe, expect, it, vi } from "vitest";
import {
  CANVAS_FORMAT_ERROR,
  COLABONE_CANONICAL_VISUAL_PROMPT,
  COLABONE_LEGACY_VISUAL_PROMPTS,
  buildColabOneWorkflowOverrides,
  defaultFieldValues,
  extractPublicHost,
  formatOutputSize,
  groupWorkflowFields,
  hasNonCommercialNotice,
  isColabOnePresetVisualPrompt,
  isColabOneVoiceRefRequired,
  isWorkflowFieldVisible,
  missingRequiredWorkflowFields,
  outputBlobURL,
  parseWorkflowImportJSON,
  partitionWorkflowFields,
  remapColabOnePresetPromptFieldsForLocale,
  resolveColabOneWorkflowFieldDefault,
  resolveColabOneWorkflowFieldPlaceholder,
  resolveSelectedTtsEngine,
  workflowModelsTotalBytes,
} from "./comfyui";
import { COLABONE_LOCALE_LIST, t, type ColabOneLocale } from "./i18n";
import type {
  ColabOneComfyNodePatch,
  ColabOneComfyPipelineSpec,
  ColabOneComfyTtsEngineSpec,
  ColabOneComfyTtsVoicePreset,
  ComfyNodePatch,
  ComfyPipelineSpec,
  ComfyTtsEngineSpec,
  ComfyTtsVoicePreset,
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
      steps: 0,
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
    expect(isWorkflowFieldVisible(ttsField, {})).toBe(false);

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

    // Non-indextts2 engines do NOT require voice_ref_audio even under "custom"
    for (const engineId of ["voxcpm2", "qwen3_tts", "fish_s2_pro"]) {
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
