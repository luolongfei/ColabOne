import { describe, it, expect } from "vitest";
import {
  canColabOneLoadModel,
  createColabOneSnapshot,
  type ComfyStatus,
  type ModelSpec,
  type WorkflowOutput,
  type WorkflowTemplate,
} from "./types";
import { locationShort, locationFull, locationTooltip } from "./location";

const model: ModelSpec = {
  id: "test",
  kind: "chat",
  name: "test",
  description: "",
  downloaded: true,
  loaded: false,
  size: 1,
  min_vram_mib: 1,
  files: [],
};

describe("createColabOneSnapshot shape", () => {
  it("initializes workflows and comfy_outputs without legacy conversations or creations", () => {
    const snap = createColabOneSnapshot();
    expect(snap.workflows).toEqual([]);
    expect(snap.comfy_outputs).toEqual([]);
    expect("conversations" in snap).toBe(false);
    expect("creations" in snap).toBe(false);
    expect("stream" in snap).toBe(false);
  });

  it("supports ComfyUI runtime status, workflows, and outputs", () => {
    const snap = createColabOneSnapshot();
    const comfy: ComfyStatus = {
      installed: true,
      running: true,
      state: "ready",
      port: 8188,
      tag: "v0.46.0",
      queue_running: 1,
      queue_pending: 0,
      error: "",
      canvas: {
        state: "ready",
        enabled: true,
        mode: "named",
        url: "https://comfy.example.com",
        error: "",
        checked_at: 1_700_000_000,
        active_connections: 1,
      },
    };
    const workflow: WorkflowTemplate = {
      id: "flux-demo",
      name: "Flux Demo",
      description: "Test workflow",
      source: "custom",
      min_vram_mib: 12288,
      node_count: 5,
      fields: [
        {
          key: "6.text",
          label: "提示词",
          node_id: "6",
          input_name: "text",
          field_type: "textarea",
          default: "mountain lake",
        },
      ],
    };
    const output: WorkflowOutput = {
      id: "0123456789abcdef0123456789abcdef",
      workflow_id: "flux-demo",
      workflow_name: "Flux Demo",
      kind: "image",
      filename: "out.png",
      extension: "png",
      mime: "image/png",
      size: 2048,
      created_at: 1_700_000_000,
      elapsed: 4.2,
    };
    snap.runtime.comfyui = comfy;
    snap.runtime.active_engine = "comfyui";
    snap.workflows = [workflow];
    snap.comfy_outputs = [output];

    expect(snap.runtime.active_engine).toBe("comfyui");
    expect(snap.runtime.comfyui.canvas?.state).toBe("ready");
    expect(snap.workflows).toHaveLength(1);
    expect(snap.comfy_outputs).toHaveLength(1);
  });
});

describe("remote-only operation gates", () => {
  it("never loads without an active remote device", () =>
    expect(canColabOneLoadModel(model, createColabOneSnapshot())).toBe(false));
  it("requires files on this runtime", () => {
    const state = createColabOneSnapshot();
    state.runtime.state = "ready";
    expect(canColabOneLoadModel({ ...model, downloaded: false }, state)).toBe(false);
  });
  it("blocks a model switch during public API or ComfyUI execution", () => {
    const state = createColabOneSnapshot();
    state.runtime.state = "ready";
    state.runtime.tasks = [
      {
        id: "task",
        kind: "public_chat",
        model_id: "other",
        state: "running",
        progress: 0,
        message: "",
        error: "",
      },
    ];
    expect(canColabOneLoadModel(model, state)).toBe(false);

    state.runtime.tasks = [
      {
        id: "task-comfy",
        kind: "comfyui_run",
        model_id: "",
        state: "running",
        progress: 0.4,
        message: "运行工作流",
        error: "",
      },
    ];
    expect(canColabOneLoadModel(model, state)).toBe(false);
  });
  it("permits a downloaded model when the remote device is idle", () => {
    const state = createColabOneSnapshot();
    state.runtime.state = "ready";
    expect(canColabOneLoadModel(model, state)).toBe(true);
  });
  it("formats instance country, subregion and zone labels cleanly", () => {
    const us = {
      country: "US",
      country_name: "美国",
      region: "俄勒冈",
      zone: "us-west1-b",
    };
    expect(locationShort(us)).toBe("美国");
    expect(locationFull(us)).toBe("美国 · 俄勒冈");
    expect(locationTooltip(us)).toBe("实例位置：美国 · 俄勒冈 (us-west1-b)");

    const sg = { country: "SG", country_name: "新加坡", region: "新加坡" };
    expect(locationFull(sg)).toBe("新加坡");
    expect(locationShort(null)).toBe("");
  });
});
