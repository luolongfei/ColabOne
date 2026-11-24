import { describe, it, expect } from "vitest";
import {
  canColabOneLoadModel,
  COLABONE_DEFAULT_GPU,
  createColabOneSnapshot,
  COLABONE_STATUS_LABELS,
  type CommonRepo,
  type InstanceLocation,
  type ModelSpec,
  type RuntimeStatus,
  type ColabOneSnapshot,
  type TaskStatus,
} from "./types";
import {
  countryLabel,
  locationShort,
  locationFull,
  locationTooltip,
} from "./location";
import {
  describeModelRemoteStatus,
  filterCommonModels,
  filterCuratedCommunityModels,
  modelKindLabel,
  normalizeModelTab,
  repoRemoteSummary,
} from "./modelLibrary";

const makeModel = (overrides: Partial<ModelSpec> = {}): ModelSpec => ({
  id: "qwen2.5-7b-instruct",
  kind: "chat",
  name: "Qwen 2.5 7B Instruct",
  description: "General-purpose chat model",
  downloaded: true,
  loaded: false,
  size: 4_700_000_000,
  min_vram_mib: 6144,
  files: [{ repo: "Qwen/Qwen2.5-7B-Instruct-GGUF", path: "model.gguf" }],
  ...overrides,
});

const makeTask = (overrides: Partial<TaskStatus> = {}): TaskStatus => ({
  id: "task-1",
  kind: "download",
  model_id: "qwen2.5-7b-instruct",
  state: "running",
  progress: 0.5,
  message: "Downloading",
  error: "",
  ...overrides,
});

const readySnapshot = (): ColabOneSnapshot => {
  const snap = createColabOneSnapshot();
  snap.runtime.state = "ready";
  return snap;
};

describe("ColabOneSnapshot & ModelSpec defaults and library gating", () => {
  it("initializes ColabOneSnapshot defaults with an empty common_repos array and isolated state", () => {
    const first = createColabOneSnapshot();
    const second = createColabOneSnapshot();

    expect(first.common_repos).toEqual([]);
    expect(Array.isArray(first.common_repos)).toBe(true);
    expect(first.models).toEqual([]);
    expect(first.workflows).toEqual([]);
    expect(first.comfy_outputs).toEqual([]);
    expect(first.logs).toEqual([]);
    expect(first.runtime).toEqual({ state: "stopped" });
    expect(first.account).toEqual({ logged_in: false });
    expect(first.operation).toBeNull();
    expect(first.error).toBe("");
    expect(first.auth_url).toBe("");
    expect(first.active_endpoint).toBe("");
    expect(first.setup).toEqual({
      colab: false,
      drive: false,
      drive_authorized: false,
      hf: false,
    });
    expect(first.assign_proxy_settings).toEqual({
      enabled: false,
      has_proxy: false,
      masked_url: "",
      has_auth: false,
    });
    expect(COLABONE_DEFAULT_GPU).toBe("G4");
    expect(first.settings).toEqual({
      gpu: "G4",
      idle_minutes: 30,
      idle_countdown_seconds: 60,
      auto_drive: true,
      download_threads: 16,
      assign_proxy_enabled: false,
    });

    // Mutating one snapshot instance must not bleed into subsequent calls
    const commonRepo: CommonRepo = {
      repo: "bartowski/Llama-3.2-3B-Instruct-GGUF",
      name: "Llama 3.2 3B",
      note: "Lightweight GGUF",
      filename: "Llama-3.2-3B-Instruct-Q4_K_M.gguf",
      downloads: 12000,
      likes: 340,
      added_at: 1_700_000_000,
    };
    first.common_repos?.push(commonRepo);
    first.models.push(makeModel({ common: true, custom: true }));
    first.runtime.state = "ready";

    expect(second.common_repos).toEqual([]);
    expect(second.models).toEqual([]);
    expect(second.runtime.state).toBe("stopped");
  });

  it("exposes localized runtime state labels", () => {
    expect(COLABONE_STATUS_LABELS.stopped).toBe("未启动");
    expect(COLABONE_STATUS_LABELS.starting).toBe("正在启动");
    expect(COLABONE_STATUS_LABELS.ready).toBe("已连接");
    expect(COLABONE_STATUS_LABELS.disconnected).toBe("连接中断");
    expect(COLABONE_STATUS_LABELS.releasing).toBe("正在释放");
    expect(COLABONE_STATUS_LABELS.failed).toBe("操作失败");
  });

  it("requires runtime state to be ready before any model can load", () => {
    const model = makeModel({ downloaded: true, loaded: false });
    for (const runtimeState of [
      "stopped",
      "starting",
      "disconnected",
      "releasing",
      "failed",
    ]) {
      const snap = createColabOneSnapshot();
      snap.runtime.state = runtimeState;
      expect(canColabOneLoadModel(model, snap)).toBe(false);
    }

    expect(canColabOneLoadModel(model, readySnapshot())).toBe(true);
  });

  it("handles downloaded and loaded combinations accurately", () => {
    const snap = readySnapshot();

    expect(canColabOneLoadModel(makeModel({ downloaded: true, loaded: false }), snap)).toBe(
      true,
    );
    expect(canColabOneLoadModel(makeModel({ downloaded: false, loaded: false }), snap)).toBe(
      false,
    );
    expect(canColabOneLoadModel(makeModel({ downloaded: true, loaded: true }), snap)).toBe(
      false,
    );
    expect(canColabOneLoadModel(makeModel({ downloaded: false, loaded: true }), snap)).toBe(
      false,
    );
  });

  it("evaluates common and custom library models using identical download/load rules", () => {
    const snap = readySnapshot();

    // Common built-in model
    expect(
      canColabOneLoadModel(
        makeModel({ common: true, custom: false, downloaded: true }),
        snap,
      ),
    ).toBe(true);
    expect(
      canColabOneLoadModel(
        makeModel({ common: true, custom: false, downloaded: false }),
        snap,
      ),
    ).toBe(false);

    // Custom Hugging Face model
    expect(
      canColabOneLoadModel(
        makeModel({
          custom: true,
          repo: "unsloth/DeepSeek-R1-Distill-Qwen-7B-GGUF",
          downloaded: true,
          loaded: false,
        }),
        snap,
      ),
    ).toBe(true);
    expect(
      canColabOneLoadModel(
        makeModel({
          custom: true,
          repo: "unsloth/DeepSeek-R1-Distill-Qwen-7B-GGUF",
          downloaded: false,
          loaded: false,
        }),
        snap,
      ),
    ).toBe(false);
    expect(
      canColabOneLoadModel(
        makeModel({
          common: true,
          custom: true,
          downloaded: true,
          loaded: true,
        }),
        snap,
      ),
    ).toBe(false);
    expect(
      canColabOneLoadModel(
        makeModel({
          common: true,
          custom: true,
          kind: "chat",
          downloaded: true,
          loaded: false,
        }),
        snap,
      ),
    ).toBe(true);
  });

  it("blocks loading when an active operation is present on the snapshot", () => {
    const model = makeModel({ common: true, downloaded: true, loaded: false });

    const runningOp = readySnapshot();
    runningOp.operation = { title: "正在切换模型", running: true };
    expect(canColabOneLoadModel(model, runningOp)).toBe(false);

    const pendingOp = readySnapshot();
    pendingOp.operation = { title: "正在整理缓存", running: false };
    expect(canColabOneLoadModel(model, pendingOp)).toBe(false);
  });

  it("blocks loading only while at least one runtime task is in running state", () => {
    const model = makeModel({ custom: true, downloaded: true, loaded: false });

    const noTasks = readySnapshot();
    expect(canColabOneLoadModel(model, noTasks)).toBe(true);

    const emptyTasks = readySnapshot();
    emptyTasks.runtime.tasks = [];
    expect(canColabOneLoadModel(model, emptyTasks)).toBe(true);

    const finishedTasks = readySnapshot();
    finishedTasks.runtime.tasks = [
      makeTask({ id: "t1", state: "completed", progress: 1 }),
      makeTask({ id: "t2", kind: "chat", state: "failed", error: "timeout" }),
      makeTask({ id: "t3", kind: "load", state: "cancelled" }),
    ];
    expect(canColabOneLoadModel(model, finishedTasks)).toBe(true);

    const activeTask = readySnapshot();
    activeTask.runtime.tasks = [
      makeTask({ id: "t1", state: "completed", progress: 1 }),
      makeTask({
        id: "t2",
        kind: "download",
        model_id: "another-model",
        state: "running",
      }),
    ];
    expect(canColabOneLoadModel(model, activeTask)).toBe(false);
  });
});

describe("GCP instance location formatting", () => {
  const gcpRegions: {
    code: string;
    expectedCountry: string;
    region: string;
    zone: string;
  }[] = [
    {
      code: "US",
      expectedCountry: "美国",
      region: "艾奥瓦",
      zone: "us-central1-a",
    },
    {
      code: "TW",
      expectedCountry: "中国台湾",
      region: "彰化",
      zone: "asia-east1-b",
    },
    {
      code: "HK",
      expectedCountry: "中国香港",
      region: "葵涌",
      zone: "asia-east2-a",
    },
    {
      code: "SG",
      expectedCountry: "新加坡",
      region: "裕廊西",
      zone: "asia-southeast1-c",
    },
    {
      code: "JP",
      expectedCountry: "日本",
      region: "东京",
      zone: "asia-northeast1-a",
    },
    {
      code: "KR",
      expectedCountry: "韩国",
      region: "首尔",
      zone: "asia-northeast3-b",
    },
    {
      code: "NL",
      expectedCountry: "荷兰",
      region: "埃姆斯哈文",
      zone: "europe-west4-a",
    },
    {
      code: "BE",
      expectedCountry: "比利时",
      region: "圣吉兰",
      zone: "europe-west1-d",
    },
    {
      code: "DE",
      expectedCountry: "德国",
      region: "法兰克福",
      zone: "europe-west3-c",
    },
    {
      code: "GB",
      expectedCountry: "英国",
      region: "伦敦",
      zone: "europe-west2-b",
    },
    {
      code: "FR",
      expectedCountry: "法国",
      region: "巴黎",
      zone: "europe-west9-a",
    },
    {
      code: "AU",
      expectedCountry: "澳大利亚",
      region: "悉尼",
      zone: "australia-southeast1-a",
    },
    {
      code: "BR",
      expectedCountry: "巴西",
      region: "圣保罗",
      zone: "southamerica-east1-b",
    },
    {
      code: "IN",
      expectedCountry: "印度",
      region: "孟买",
      zone: "asia-south1-a",
    },
  ];

  it.each(gcpRegions)(
    "formats country, full location, and tooltip for GCP region $code ($zone)",
    ({ code, expectedCountry, region, zone }) => {
      const loc: InstanceLocation = {
        country: code,
        country_name: code, // overrides take precedence even when country_name is raw ISO code
        region,
        zone,
      };

      expect(countryLabel(loc)).toBe(expectedCountry);
      expect(locationShort(loc)).toBe(expectedCountry);
      expect(locationFull(loc)).toBe(`${expectedCountry} · ${region}`);
      expect(locationTooltip(loc)).toBe(
        `实例位置：${expectedCountry} · ${region} (${zone})`,
      );
    },
  );

  it("normalizes lowercase and padded ISO country codes", () => {
    const loc: InstanceLocation = {
      country: "  tw ",
      country_name: "Taiwan",
      region: "  彰化  ",
      zone: "asia-east1-a",
    };
    expect(countryLabel(loc)).toBe("中国台湾");
    expect(locationShort(loc)).toBe("中国台湾");
    expect(locationFull(loc)).toBe("中国台湾 · 彰化");
    expect(locationTooltip(loc)).toBe(
      "实例位置：中国台湾 · 彰化 (asia-east1-a)",
    );
  });

  it("deduplicates locationFull when region matches country label or is empty", () => {
    // City-state / SAR cases where region === resolved country label
    const sgSame: InstanceLocation = {
      country: "SG",
      country_name: "新加坡",
      region: "新加坡",
      zone: "asia-southeast1-a",
    };
    expect(locationFull(sgSame)).toBe("新加坡");
    expect(locationTooltip(sgSame)).toBe(
      "实例位置：新加坡 (asia-southeast1-a)",
    );

    const hkSame: InstanceLocation = {
      country: "HK",
      country_name: "中国香港",
      region: "  中国香港  ",
      zone: "asia-east2-b",
    };
    expect(locationFull(hkSame)).toBe("中国香港");
    expect(locationTooltip(hkSame)).toBe("实例位置：中国香港 (asia-east2-b)");

    // Unmapped country where region === country_name
    const luSame: InstanceLocation = {
      country: "LU",
      country_name: "卢森堡",
      region: "卢森堡",
    };
    expect(locationFull(luSame)).toBe("卢森堡");
    expect(locationTooltip(luSame)).toBe("实例位置：卢森堡");

    // Missing, empty, or whitespace-only region
    expect(locationFull({ country: "DE" })).toBe("德国");
    expect(locationFull({ country: "DE", region: "" })).toBe("德国");
    expect(locationFull({ country: "DE", region: "   " })).toBe("德国");
    expect(locationTooltip({ country: "DE", region: "" })).toBe(
      "实例位置：德国",
    );
  });

  it("resolves Nordic and unmapped ISO codes via overrides, country_name, or Intl.DisplayNames", () => {
    const zhRegionNames = new Intl.DisplayNames(["zh-CN"], { type: "region" });

    // DK (Denmark) resolves cleanly to Chinese
    expect(countryLabel({ country: "DK" })).toBe("丹麦");
    expect(locationFull({ country: "DK", region: "哥本哈根" })).toBe(
      "丹麦 · 哥本哈根",
    );

    expect(countryLabel({ country: "CZ" })).toBe("捷克");
    expect(countryLabel({ country: "RO" })).toBe("罗马尼亚");
    expect(countryLabel({ country: "HU" })).toBe("匈牙利");

    // Unmapped ISO codes like IS, SK, BG fall back to Intl.DisplayNames(["zh-CN"])
    for (const code of ["IS", "SK", "BG"]) {
      const expected = zhRegionNames.of(code) || code;
      expect(countryLabel({ country: code })).toBe(expected);
      expect(
        countryLabel({ country: code.toLowerCase(), country_name: code }),
      ).toBe(expected);
      expect(expected).not.toBe(code);
    }

    // Explicit localized country_name on an unmapped code takes precedence before Intl fallback
    const customUnmapped: InstanceLocation = {
      country: "IS",
      country_name: "冰岛共和国",
      region: "雷克雅未克",
      zone: "europe-north-is1",
    };
    expect(countryLabel(customUnmapped)).toBe("冰岛共和国");
    expect(locationFull(customUnmapped)).toBe("冰岛共和国 · 雷克雅未克");
    expect(locationTooltip(customUnmapped)).toBe(
      "实例位置：冰岛共和国 · 雷克雅未克 (europe-north-is1)",
    );
  });

  it("handles null, undefined, empty, whitespace, and malformed country codes gracefully", () => {
    for (const emptyInput of [
      undefined,
      null,
      { country: "" },
      {
        country: "",
        country_name: "美国",
        region: "俄勒冈",
        zone: "us-west1-a",
      },
      { country: "   " },
    ] as (InstanceLocation | null | undefined)[]) {
      expect(countryLabel(emptyInput)).toBe("");
      expect(locationShort(emptyInput)).toBe("");
      expect(locationFull(emptyInput)).toBe("");
      expect(locationTooltip(emptyInput)).toBe("");
    }

    // Malformed non-ISO strings that cause Intl.DisplayNames.of() to throw RangeError
    expect(() => countryLabel({ country: "INVALID_999!" })).not.toThrow();
    expect(countryLabel({ country: "INVALID_999!" })).toBe("INVALID_999!");
    expect(
      countryLabel({ country: "INVALID_999!", country_name: "未知区域" }),
    ).toBe("未知区域");
  });
});

describe("Two-tab model library helpers (modelLibrary)", () => {
  it("normalizes legacy and unknown tab values into the two remaining tabs ('common' | 'hub')", () => {
    expect(normalizeModelTab("hub")).toBe("hub");
    expect(normalizeModelTab("common")).toBe("common");
    expect(normalizeModelTab("starred")).toBe("common");
    expect(normalizeModelTab("mine")).toBe("common");
    expect(normalizeModelTab("installed")).toBe("common");
    expect(normalizeModelTab("creative")).toBe("common");
    expect(normalizeModelTab("")).toBe("common");
    expect(normalizeModelTab(undefined)).toBe("common");
    expect(normalizeModelTab(null)).toBe("common");
  });

  it("formats localized model kind labels", () => {
    expect(modelKindLabel("chat")).toBe("文本对话");
    expect(modelKindLabel(undefined)).toBe("文本对话");
  });

  it("includes common, downloaded, and loaded models in filterCommonModels, sorted loaded -> downloaded -> common, and excludes removed custom models", () => {
    const runtime: RuntimeStatus = {
      state: "ready",
      loaded_model: "vram-active",
    };

    const untouchedBuiltIn = makeModel({
      id: "builtin-not-common",
      name: "Built-in Not Common",
      common: false,
      custom: false,
      downloaded: false,
      loaded: false,
    });
    const customRemovedFromCommon = makeModel({
      id: "custom-removed",
      name: "Custom Removed",
      repo: "unsloth/Removed-Model-GGUF",
      common: false,
      custom: true,
      downloaded: false,
      loaded: false,
    });
    const customCommon = makeModel({
      id: "custom-common",
      name: "Custom Imported",
      repo: "unsloth/Mistral-Nemo-GGUF",
      common: true,
      custom: true,
      downloaded: false,
      loaded: false,
    });
    const commonOnly = makeModel({
      id: "common-only",
      name: "Common Cloud Model",
      common: true,
      custom: false,
      downloaded: false,
      loaded: false,
    });
    const downloadedModel = makeModel({
      id: "downloaded-only",
      name: "Cached Remote Model",
      common: false,
      custom: false,
      downloaded: true,
      loaded: false,
    });
    const loadedModel = makeModel({
      id: "vram-active",
      name: "Active VRAM Model",
      common: false,
      custom: false,
      downloaded: true,
      loaded: false, // active via runtime.loaded_model
    });

    const input = [
      untouchedBuiltIn,
      customRemovedFromCommon,
      customCommon,
      commonOnly,
      downloadedModel,
      loadedModel,
    ];
    const result = filterCommonModels(input, "", runtime);

    expect(result.map((m) => m.id)).toEqual([
      "vram-active",
      "downloaded-only",
      "custom-common",
      "common-only",
    ]);

    // Search query filters across name, description, repo, and localized kind label
    expect(
      filterCommonModels(input, "  mistral-nemo  ", runtime).map((m) => m.id),
    ).toEqual(["custom-common"]);
    expect(
      filterCommonModels(input, "active vram", runtime).map((m) => m.id),
    ).toEqual(["vram-active"]);

    const withCommonAlt = [
      ...input,
      makeModel({
        id: "deepseek-common",
        kind: "chat",
        name: "DeepSeek R1",
        description: "中文推理模型",
        common: true,
        downloaded: false,
      }),
    ];
    expect(
      filterCommonModels(withCommonAlt, "中文推理", runtime).map((m) => m.id),
    ).toEqual(["deepseek-common"]);

    // When runtime is stopped, runtime.loaded_model or model.loaded does not count as active VRAM
    const stoppedResult = filterCommonModels(
      [
        makeModel({
          id: "only-marked-loaded",
          downloaded: false,
          common: false,
          custom: false,
          loaded: true,
        }),
      ],
      "",
      { state: "stopped", loaded_model: "only-marked-loaded" },
    );
    expect(stoppedResult).toEqual([]);
  });

  it("filters curated community models excluding custom imports and matching search query", () => {
    const models = [
      makeModel({
        id: "curated-qwen",
        name: "Qwen 2.5 7B",
        custom: false,
        common: false,
      }),
      makeModel({
        id: "curated-gemma",
        kind: "chat",
        name: "Gemma 4 31B",
        description: "高质量长文本推理",
        repo: "mradermacher/gemma-4-31B-GGUF",
        files: [{ repo: "mradermacher/gemma-4-31B-GGUF", path: "gemma.gguf" }],
        custom: false,
      }),
      makeModel({
        id: "user-custom",
        name: "Qwen Custom Import",
        custom: true,
      }),
    ];

    expect(filterCuratedCommunityModels(models, "").map((m) => m.id)).toEqual([
      "curated-qwen",
      "curated-gemma",
    ]);
    expect(
      filterCuratedCommunityModels(models, "qwen").map((m) => m.id),
    ).toEqual(["curated-qwen"]);
    expect(
      filterCuratedCommunityModels(models, "长文本推理").map((m) => m.id),
    ).toEqual(["curated-gemma"]);
  });

  it("distinguishes loaded, loading, downloading, downloaded, remote_unfetched, and offline remote statuses", () => {
    const model = makeModel({
      id: "deepseek-r1",
      downloaded: true,
      loaded: false,
    });

    // 1. loaded
    const loadedBadge = describeModelRemoteStatus(
      { ...model, loaded: true },
      { state: "ready" },
    );
    expect(loadedBadge).toMatchObject({
      state: "loaded",
      label: "已装载显存",
      tone: "green",
    });

    // Also loaded via runtime.loaded_model
    expect(
      describeModelRemoteStatus(model, {
        state: "ready",
        loaded_model: "deepseek-r1",
      }),
    ).toMatchObject({
      state: "loaded",
      label: "已装载显存",
    });

    // 2. loading
    const loadingBadge = describeModelRemoteStatus(model, {
      state: "ready",
      tasks: [
        makeTask({
          kind: "load",
          model_id: "deepseek-r1",
          state: "running",
          message: "正在分配 KV Cache",
        }),
      ],
    });
    expect(loadingBadge).toMatchObject({
      state: "loading",
      label: "正在装载显存…",
      tone: "amber",
      detail: "正在分配 KV Cache",
    });

    // 3. downloading (with percentage)
    const downloadingBadge = describeModelRemoteStatus(
      { ...model, downloaded: false },
      {
        state: "ready",
        tasks: [
          makeTask({
            kind: "download",
            model_id: "deepseek-r1",
            state: "running",
            progress: 0.426,
            message: "分片 1/2 下载中",
          }),
        ],
      },
    );
    expect(downloadingBadge).toMatchObject({
      state: "downloading",
      label: "远端下载中 43%",
      tone: "amber",
      progress: 0.426,
      detail: "分片 1/2 下载中",
    });

    // 4. downloaded
    const downloadedBadge = describeModelRemoteStatus(model, {
      state: "ready",
    });
    expect(downloadedBadge).toMatchObject({
      state: "downloaded",
      label: "已下载到远端",
      tone: "blue",
    });

    // 5. remote_unfetched
    const unfetchedBadge = describeModelRemoteStatus(
      { ...model, downloaded: false },
      { state: "ready" },
    );
    expect(unfetchedBadge).toMatchObject({
      state: "remote_unfetched",
      label: "未下载到远端",
      tone: "muted",
    });

    // 6. offline
    const offlineUnfetched = describeModelRemoteStatus(
      { ...model, downloaded: false },
      { state: "stopped" },
    );
    expect(offlineUnfetched).toMatchObject({
      state: "offline",
      label: "待启动远端 GPU",
      tone: "muted",
    });

    const offlineCached = describeModelRemoteStatus(
      { ...model, downloaded: true, loaded: true },
      { state: "stopped" },
    );
    expect(offlineCached).toMatchObject({
      state: "offline",
      label: "已下载到远端",
      tone: "blue",
    });
  });

  it("summarizes Hugging Face repo remote status case-insensitively", () => {
    const models: ModelSpec[] = [
      makeModel({
        id: "q4-variant",
        name: "Qwen 2.5 7B Q4_K_M",
        repo: "Qwen/Qwen2.5-7B-Instruct-GGUF",
        common: true,
        downloaded: true,
        loaded: false,
      }),
      makeModel({
        id: "q8-variant",
        name: "Qwen 2.5 7B Q8_0",
        repo: undefined,
        files: [
          { repo: "qwen/qwen2.5-7b-instruct-gguf", path: "model-q8.gguf" },
        ],
        common: false,
        downloaded: true,
        loaded: true,
      }),
      makeModel({
        id: "other-repo",
        name: "Llama 3.2 3B",
        repo: "bartowski/Llama-3.2-3B-Instruct-GGUF",
        common: true,
        downloaded: true,
        loaded: false,
      }),
    ];

    const activeSummary = repoRemoteSummary(
      "  QWEN/Qwen2.5-7B-Instruct-GGUF  ",
      models,
      { state: "ready" },
    );
    expect(activeSummary.common).toBe(true);
    expect(activeSummary.downloadedCount).toBe(2);
    expect(activeSummary.loadedName).toBe("Qwen 2.5 7B Q8_0");
    expect(activeSummary.savedModels).toHaveLength(2);

    // Stopped runtime suppresses loadedName
    const stoppedSummary = repoRemoteSummary(
      "qwen/qwen2.5-7b-instruct-gguf",
      models,
      { state: "stopped" },
    );
    expect(stoppedSummary.common).toBe(true);
    expect(stoppedSummary.downloadedCount).toBe(2);
    expect(stoppedSummary.loadedName).toBeUndefined();

    // Unknown or empty repo
    expect(
      repoRemoteSummary("unknown/model-gguf", models, { state: "ready" }),
    ).toEqual({
      common: false,
      downloadedCount: 0,
      savedModels: [],
    });
    expect(repoRemoteSummary("   ", models, { state: "ready" })).toEqual({
      common: false,
      downloadedCount: 0,
      savedModels: [],
    });
  });
});
