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

  it("treats protocol < 7 as non-dismissible protocol upgrade and requests confirmation when active", () => {
    const legacy = createColabOneSnapshot();
    legacy.runtime.state = "ready";
    legacy.runtime.id = "gpu-legacy";
    legacy.runtime.protocol = 6;
    legacy.runtime.loaded_model = "qwen";

    const active = evaluateColabOneRuntimeUpgrade(legacy);
    expect(active.visible).toBe(true);
    expect(active.kind).toBe("protocol");
    expect(active.dismissible).toBe(false);
    expect(active.canUpgrade).toBe(true);
    expect(active.needsConfirm).toBe(true);
    expect(active.activeItems).toContain("已装载模型：qwen（更新后将按原规格自动重新装载）");
    expect(active.message).toContain("实时硬件监控");
    expect(active.disabledReason).toBe("");

    legacy.runtime.loaded_model = "";
    const ready = evaluateColabOneRuntimeUpgrade(legacy);
    expect(ready.canUpgrade).toBe(true);
    expect(ready.needsConfirm).toBe(false);
    expect(ready.activeItems).toEqual([]);
    expect(ready.disabledReason).toBe("");
  });

  it("treats protocol >= 7 revision mismatch as dismissible optional update that confirms active items and only disables during operations", () => {
    const state = createColabOneSnapshot();
    state.runtime.state = "ready";
    state.runtime.id = "gpu-g4-1";
    state.runtime.protocol = 7;
    state.runtime.service_revision = "rev-old-1";
    state.runtime.upgrade_available = true;
    state.runtime.loaded_model = "qwen-flash";
    state.runtime.public_api = {
      enabled: true,
      mode: "named",
      state: "ready",
      url: "https://api.example.com",
      error: "",
      streaming: true,
      checked_at: 0,
      requests: 0,
      active: 0,
      completed: 0,
      failed: 0,
      last_request: 0,
      recent_requests: [],
    };

    const withModelAndApi = evaluateColabOneRuntimeUpgrade(state);
    expect(withModelAndApi.visible).toBe(true);
    expect(withModelAndApi.kind).toBe("optional");
    expect(withModelAndApi.dismissible).toBe(true);
    expect(withModelAndApi.canUpgrade).toBe(true);
    expect(withModelAndApi.needsConfirm).toBe(true);
    expect(withModelAndApi.disabledReason).toBe("");
    expect(withModelAndApi.message).toContain("不影响当前正常使用");
    expect(withModelAndApi.message).toContain("自动按当前配置恢复模型与服务");
    expect(withModelAndApi.message).not.toContain("以启用实时硬件监控");
    expect(withModelAndApi.activeItems).toContain(
      "已装载模型：qwen-flash（更新后将按原规格自动重新装载）",
    );
    expect(withModelAndApi.activeItems).toContain(
      "对外 API 服务与公网隧道（更新后将按原配置自动重新开启）",
    );

    state.runtime.loaded_model = "";
    state.runtime.public_api = undefined;
    state.runtime.active_engine = "comfyui";
    const withComfy = evaluateColabOneRuntimeUpgrade(state);
    expect(withComfy.canUpgrade).toBe(true);
    expect(withComfy.needsConfirm).toBe(true);
    expect(withComfy.activeItems).toContain(
      "ComfyUI 工作流引擎与画布隧道（更新后将自动重新启动）",
    );

    state.runtime.active_engine = null;
    state.runtime.comfyui = {
      installed: true,
      running: false,
      state: "starting",
      queue_running: 0,
      queue_pending: 0,
      error: "",
      canvas: {
        state: "ready",
        enabled: true,
        mode: "named",
        url: "https://canvas.example.com",
        error: "",
        checked_at: 0,
      },
    };
    const withStartingComfyCanvas = evaluateColabOneRuntimeUpgrade(state);
    expect(withStartingComfyCanvas.canUpgrade).toBe(true);
    expect(withStartingComfyCanvas.needsConfirm).toBe(true);
    expect(withStartingComfyCanvas.activeItems).toContain(
      "ComfyUI 工作流引擎与画布隧道（更新后将自动重新启动）",
    );

    state.runtime.comfyui = undefined;
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
    expect(withTask.canUpgrade).toBe(true);
    expect(withTask.needsConfirm).toBe(true);
    expect(withTask.activeItems).toContain("正在运行中的远端任务（将被取消）");

    state.operation = { title: "正在更新远端服务", running: true };
    const duringOp = evaluateColabOneRuntimeUpgrade(state);
    expect(duringOp.canUpgrade).toBe(false);
    expect(duringOp.disabledReason).toContain("当前操作完成");

    state.operation = null;
    state.runtime.tasks = [];
    const idle = evaluateColabOneRuntimeUpgrade(state);
    expect(idle.canUpgrade).toBe(true);
    expect(idle.needsConfirm).toBe(false);
    expect(idle.activeItems).toEqual([]);
    expect(idle.disabledReason).toBe("");
    expect(idle.message).toContain("随时热更新");
    expect(idle.dismissKey).toBe("optional::gpu-g4-1::rev-old-1");

    state.runtime.id = "gpu-g4-2";
    expect(evaluateColabOneRuntimeUpgrade(state).dismissKey).toBe(
      "optional::gpu-g4-2::rev-old-1",
    );
  });

  it("evaluates Live Studio running/transitional states independently of ComfyUI canvas and supports llama+live_studio", () => {
    const state = createColabOneSnapshot();
    state.runtime.state = "ready";
    state.runtime.id = "gpu-live-1";
    state.runtime.protocol = 7;
    state.runtime.service_revision = "rev-old-live";
    state.runtime.upgrade_available = true;

    // 1) Co-active ComfyUI canvas + running Live Studio must list BOTH in activeItems
    state.runtime.active_engine = "live_studio";
    state.runtime.comfyui = {
      installed: true,
      running: false,
      state: "stopped",
      queue_running: 0,
      queue_pending: 0,
      error: "",
      canvas: {
        state: "ready",
        enabled: true,
        mode: "named",
        url: "https://canvas.example.com",
        error: "",
        checked_at: 0,
      },
    };
    state.runtime.live_studio = {
      running: true,
      state: "ready",
    };
    const comfyAndLive = evaluateColabOneRuntimeUpgrade(state);
    expect(comfyAndLive.needsConfirm).toBe(true);
    expect(comfyAndLive.activeItems).toContain(
      "ComfyUI 工作流引擎与画布隧道（更新后将自动重新启动）",
    );
    expect(comfyAndLive.activeItems).toContain(
      "当前运行中的远端推理引擎（更新后将自动恢复）",
    );

    // 2) Live Studio starting or stopping without active_engine still requires confirmation
    state.runtime.active_engine = null;
    state.runtime.comfyui = undefined;
    state.runtime.live_studio = {
      running: false,
      state: "starting",
    };
    const startingLive = evaluateColabOneRuntimeUpgrade(state);
    expect(startingLive.needsConfirm).toBe(true);
    expect(startingLive.activeItems).toContain(
      "当前运行中的远端推理引擎（更新后将自动恢复）",
    );

    state.runtime.live_studio = {
      running: false,
      state: "stopping",
    };
    const stoppingLive = evaluateColabOneRuntimeUpgrade(state);
    expect(stoppingLive.needsConfirm).toBe(true);
    expect(stoppingLive.activeItems).toContain(
      "当前运行中的远端推理引擎（更新后将自动恢复）",
    );

    // 3) Co-located llama+live_studio lists both model and active engine
    state.runtime.active_engine = "llama+live_studio";
    state.runtime.loaded_model = "qwen-live";
    state.runtime.live_studio = {
      running: true,
      state: "ready",
    };
    const colocated = evaluateColabOneRuntimeUpgrade(state);
    expect(colocated.needsConfirm).toBe(true);
    expect(colocated.activeItems).toContain(
      "已装载模型：qwen-live（更新后将按原规格自动重新装载）",
    );
    expect(colocated.activeItems).toContain(
      "当前运行中的远端推理引擎（更新后将自动恢复）",
    );

    // 4) Stopped Live Studio does not trigger confirmation
    state.runtime.active_engine = null;
    state.runtime.loaded_model = "";
    state.runtime.live_studio = {
      running: false,
      state: "stopped",
    };
    const stoppedLive = evaluateColabOneRuntimeUpgrade(state);
    expect(stoppedLive.needsConfirm).toBe(false);
    expect(stoppedLive.activeItems).toEqual([]);
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
