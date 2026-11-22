import { describe, it, expect } from "vitest";
import {
  COLABONE_AUTO_API_MODEL_ID,
  canColabOneLoadModel,
  colabOneModelApiId,
  colabOneModelQuantTag,
  createColabOneSnapshot,
  evaluateColabOneRuntimeUpgrade,
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

describe("remote service upgrade banner classification", () => {
  it("stays hidden when runtime is stopped or up to date on protocol >= 7", () => {
    const stopped = createColabOneSnapshot();
    expect(evaluateColabOneRuntimeUpgrade(stopped).visible).toBe(false);

    const fresh = createColabOneSnapshot();
    fresh.runtime.state = "ready";
    fresh.runtime.protocol = 7;
    fresh.runtime.upgrade_available = false;
    expect(evaluateColabOneRuntimeUpgrade(fresh)).toMatchObject({
      visible: false,
      kind: "none",
      dismissible: false,
    });
  });

  it("treats protocol < 7 as non-dismissible protocol upgrade with blocker tooltips", () => {
    const legacy = createColabOneSnapshot();
    legacy.runtime.state = "ready";
    legacy.runtime.id = "gpu-legacy";
    legacy.runtime.protocol = 6;
    legacy.runtime.loaded_model = "qwen";

    const blocked = evaluateColabOneRuntimeUpgrade(legacy);
    expect(blocked.visible).toBe(true);
    expect(blocked.kind).toBe("protocol");
    expect(blocked.dismissible).toBe(false);
    expect(blocked.canUpgrade).toBe(false);
    expect(blocked.message).toContain("实时硬件监控");
    expect(blocked.disabledReason).toContain("卸载模型");

    legacy.runtime.loaded_model = "";
    const ready = evaluateColabOneRuntimeUpgrade(legacy);
    expect(ready.canUpgrade).toBe(true);
    expect(ready.disabledReason).toBe("");
  });

  it("treats protocol >= 7 revision mismatch as dismissible optional update with specific blocker messages", () => {
    const state = createColabOneSnapshot();
    state.runtime.state = "ready";
    state.runtime.id = "gpu-g4-1";
    state.runtime.protocol = 7;
    state.runtime.service_revision = "rev-old-1";
    state.runtime.upgrade_available = true;
    state.runtime.loaded_model = "qwen-flash";

    const withModel = evaluateColabOneRuntimeUpgrade(state);
    expect(withModel.visible).toBe(true);
    expect(withModel.kind).toBe("optional");
    expect(withModel.dismissible).toBe(true);
    expect(withModel.canUpgrade).toBe(false);
    expect(withModel.message).toContain("不影响当前正常使用");
    expect(withModel.message).toContain("卸载模型");
    expect(withModel.message).not.toContain("以启用实时硬件监控");
    expect(withModel.disabledReason).toContain("卸载模型");

    state.runtime.loaded_model = "";
    state.runtime.active_engine = "comfyui";
    const withComfy = evaluateColabOneRuntimeUpgrade(state);
    expect(withComfy.canUpgrade).toBe(false);
    expect(withComfy.message).toContain("ComfyUI");
    expect(withComfy.disabledReason).toContain("ComfyUI");

    state.runtime.active_engine = null;
    state.runtime.tasks = [
      {
        id: "t-dl",
        kind: "download",
        model_id: "qwen",
        state: "running",
        progress: 0.5,
        message: "downloading",
        error: "",
      },
    ];
    const withTask = evaluateColabOneRuntimeUpgrade(state);
    expect(withTask.canUpgrade).toBe(false);
    expect(withTask.message).toContain("任务结束");
    expect(withTask.disabledReason).toContain("远端任务");

    state.runtime.tasks = [];
    const idle = evaluateColabOneRuntimeUpgrade(state);
    expect(idle.canUpgrade).toBe(true);
    expect(idle.disabledReason).toBe("");
    expect(idle.message).toContain("随时热更新");
    expect(idle.dismissKey).toBe("optional::gpu-g4-1::rev-old-1");

    state.runtime.id = "gpu-g4-2";
    expect(evaluateColabOneRuntimeUpgrade(state).dismissKey).toBe(
      "optional::gpu-g4-2::rev-old-1",
    );
  });
});

describe("public API model ID and quantization helpers", () => {
  it("prefers readable api_id and falls back to internal id", () => {
    expect(COLABONE_AUTO_API_MODEL_ID).toBe("colabone/auto");
    expect(
      colabOneModelApiId({
        id: "hf-82b42fca20f78bbf",
        api_id: "colabone/qwen3.8-flash-next",
      }),
    ).toBe("colabone/qwen3.8-flash-next");
    expect(colabOneModelApiId({ id: "gemma4-31b" })).toBe("gemma4-31b");
    expect(colabOneModelApiId({ id: "gemma4-31b", api_id: "   " })).toBe(
      "gemma4-31b",
    );
    expect(colabOneModelApiId(undefined)).toBe("");
  });

  it("extracts concise quantization badge from description or filename", () => {
    expect(
      colabOneModelQuantTag({
        description: "UD-Q4_K_XL · Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00004.gguf",
        files: [],
      }),
    ).toBe("UD-Q4_K_XL");
    expect(
      colabOneModelQuantTag({
        description: "27B 稠密 / BF16",
        files: [{ repo: "org/repo", path: "RVN-BF16.gguf" }],
      }),
    ).toBe("BF16");
    expect(
      colabOneModelQuantTag({
        description: "",
        files: [{ repo: "org/repo", path: "gemma-4-31B.Q8_0.gguf" }],
      }),
    ).toBe("Q8_0");
    expect(
      colabOneModelQuantTag({
        description: "",
        files: [{ repo: "org/repo", path: "Qwen3_32B_Q4_K_M_00001-of-00004.gguf" }],
      }),
    ).toBe("Q4_K_M");
    expect(
      colabOneModelQuantTag({
        description: "",
        files: [{ repo: "org/repo", path: "gpt-oss-20b-MXFP4_MOE.gguf" }],
      }),
    ).toBe("MXFP4_MOE");
    expect(
      colabOneModelQuantTag({
        description: "320B-A18B MoE / GSQ-RCO-Q4 / 部分专家放内存",
        files: [{ repo: "org/repo", path: "Q4/GLM-5.3-Flash-Q4.gguf" }],
      }),
    ).toBe("Q4");
    expect(colabOneModelQuantTag({ description: "plain", files: [] })).toBe("");
  });
});
