import { afterEach, describe, expect, it, vi } from "vitest";
import {
  CANVAS_FORMAT_ERROR,
  defaultFieldValues,
  extractPublicHost,
  formatOutputSize,
  outputBlobURL,
  parseWorkflowImportJSON,
} from "./comfyui";
import type { WorkflowField } from "./types";

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
});

describe("formatOutputSize", () => {
  it("formats bytes, kilobytes, and megabytes cleanly", () => {
    expect(formatOutputSize(0)).toBe("0 B");
    expect(formatOutputSize(-10)).toBe("0 B");
    expect(formatOutputSize(Number.NaN)).toBe("0 B");
    expect(formatOutputSize(512)).toBe("512 B");
    expect(formatOutputSize(1023)).toBe("1023 B");
    expect(formatOutputSize(1024)).toBe("1.0 KB");
    expect(formatOutputSize(1536)).toBe("1.5 KB");
    expect(formatOutputSize(1024 * 1024)).toBe("1.00 MB");
    expect(formatOutputSize(2.5 * 1024 * 1024)).toBe("2.50 MB");
  });
});

describe("outputBlobURL", () => {
  afterEach(() => {
    mockCommand.mockReset();
    vi.restoreAllMocks();
  });

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
  });

  it("blocks unsafe MIME types like SVG", async () => {
    mockCommand.mockResolvedValueOnce({
      data: btoa("<svg></svg>"),
      offset: 0,
      size: 11,
      mime: "image/svg+xml",
    });
    await expect(outputBlobURL("out_svg")).rejects.toThrow("不支持预览");
  });

  it("rejects mismatched chunk offsets", async () => {
    mockCommand.mockResolvedValueOnce({
      data: btoa("abc"),
      offset: 99,
      size: 3,
      mime: "image/png",
    });
    await expect(outputBlobURL("out_bad")).rejects.toThrow("传输不完整");
  });
});

describe("extractPublicHost", () => {
  it("extracts host and strips query parameters or tokens", () => {
    expect(
      extractPublicHost("https://comfy.example.com/_colabone_auth?token=secret123"),
    ).toBe("comfy.example.com");
    expect(extractPublicHost("")).toBe("");
  });
});
