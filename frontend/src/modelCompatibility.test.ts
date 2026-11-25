import { describe, expect, it } from "vitest";
import {
  COLABONE_HUB_FAMILIES_BY_MODALITY,
  COLABONE_PARAM_PRESETS,
  buildColabOneGpuTargetOptions,
  describeColabOneQuantization,
  describeColabOneRuntimeParams,
  estimateColabOneVramMib,
  evaluateColabOneModelDiskFit,
  evaluateColabOneTargetDiskFit,
  evaluateColabOneVramFit,
  matchColabOneParamPreset,
  planColabOneVersionOptions,
  resolveColabOneDefaultGpuTarget,
  resolveColabOneGpuTarget,
  summarizeColabOneRepoFit,
  type ColabOneHubFile,
} from "./modelCompatibility";
import {
  COLABONE_DEFAULT_GPU,
  createColabOneSnapshot,
  type ModelSpec,
  type RuntimeStatus,
} from "./types";

const makeFile = (
  filename: string,
  sizeGib: number,
  quantization: string,
  overrides: Partial<ColabOneHubFile> = {},
): ColabOneHubFile => ({
  filename,
  size: Math.round(sizeGib * 1024 ** 3),
  shards: 1,
  quantization,
  supported: true,
  reason: "",
  ...overrides,
});

describe("ColabOne GPU target resolution & VRAM fit", () => {
  it("resolves live running GPU as CURRENT with live host RAM and falls back to settings GPU when stopped", () => {
    const liveRuntime = {
      state: "ready",
      gpu: {
        name: "NVIDIA RTX PRO 6000 Blackwell",
        total_mib: 97887,
        used_mib: 1024,
      },
      resources: {
        memory: {
          total_bytes: 170 * 1024 ** 3,
          used_bytes: 12 * 1024 ** 3,
          available_bytes: 158 * 1024 ** 3,
          percent: 7.1,
        },
      },
    };
    expect(resolveColabOneDefaultGpuTarget(liveRuntime, "T4")).toBe("CURRENT");
    const liveGpu = resolveColabOneGpuTarget("CURRENT", liveRuntime, "T4");
    expect(liveGpu.isLive).toBe(true);
    expect(liveGpu.vramMib).toBe(97887);
    expect(liveGpu.hostRamGb).toBe(170);
    expect(liveGpu.shortLabel).toContain("G4");

    const options = buildColabOneGpuTargetOptions(liveRuntime);
    expect(options[0]!.id).toBe("CURRENT");
    expect(options.some((o) => o.id === "G4")).toBe(true);
    expect(options.some((o) => o.id === "H100")).toBe(true);
    expect(options.some((o) => o.id === "A100_80")).toBe(true);
    expect(options.some((o) => o.id === "ALL")).toBe(true);

    expect(
      resolveColabOneDefaultGpuTarget({ state: "stopped" }),
    ).toBe("G4");
    expect(
      resolveColabOneDefaultGpuTarget(
        { state: "stopped" },
        createColabOneSnapshot().settings.gpu,
      ),
    ).toBe(COLABONE_DEFAULT_GPU);
    expect(
      resolveColabOneDefaultGpuTarget({ state: "stopped" }, "G4"),
    ).toBe("G4");
    expect(
      resolveColabOneDefaultGpuTarget({ state: "stopped" }, "A100"),
    ).toBe("A100_80");
    expect(
      resolveColabOneDefaultGpuTarget({ state: "stopped" }, "T4"),
    ).toBe("T4");
  });

  it("classifies comfortable, tight, hybrid, heavy_hybrid, and unrestricted VRAM fit while keeping all valid oversized models selectable", () => {
    const g4 = resolveColabOneGpuTarget("G4");
    const h100 = resolveColabOneGpuTarget("H100");
    const t4 = resolveColabOneGpuTarget("T4");
    const all = resolveColabOneGpuTarget("ALL");

    // ~31 GiB Q8_0 model -> ~35 GB VRAM: comfortable on G4/H100, heavy_hybrid (still selectable) on T4 (16G VRAM + 13G RAM)
    const q8Vram = estimateColabOneVramMib(31 * 1024 ** 3, 8192);
    expect(evaluateColabOneVramFit(q8Vram, g4).status).toBe("comfortable");
    expect(evaluateColabOneVramFit(q8Vram, h100).status).toBe("comfortable");
    const q8OnT4 = evaluateColabOneVramFit(q8Vram, t4);
    expect(q8OnT4.status).toBe("heavy_hybrid");
    expect(q8OnT4.selectable).toBe(true);
    expect(q8OnT4.requiresHybrid).toBe(true);
    expect(q8OnT4.overflowGb).toBeGreaterThan(15);
    expect(q8OnT4.minGpu.id).toBe("A100_40");

    // ~20 GiB Q4 model -> ~23 GB VRAM: hybrid (fits VRAM + host RAM) on T4 16G
    const q4OnT4 = evaluateColabOneVramFit(
      estimateColabOneVramMib(20 * 1024 ** 3, 8192),
      t4,
    );
    expect(q4OnT4.status).toBe("hybrid");
    expect(q4OnT4.selectable).toBe(true);
    expect(q4OnT4.requiresHybrid).toBe(true);

    // ~72 GiB model -> ~79 GB VRAM: comfortable on G4 96G, tight/hybrid on H100 80G
    const largeVram = estimateColabOneVramMib(72 * 1024 ** 3, 8192);
    expect(evaluateColabOneVramFit(largeVram, g4).status).toBe("comfortable");
    expect(["tight", "hybrid"]).toContain(
      evaluateColabOneVramFit(largeVram, h100).status,
    );

    // ~81 GiB model -> ~89 GB VRAM: tight on G4 96G, hybrid on H100 80G
    const g4TightVram = estimateColabOneVramMib(81 * 1024 ** 3, 8192);
    expect(evaluateColabOneVramFit(g4TightVram, g4).status).toBe("tight");
    expect(evaluateColabOneVramFit(g4TightVram, g4).selectable).toBe(true);
    const g4TightOnH100 = evaluateColabOneVramFit(g4TightVram, h100);
    expect(g4TightOnH100.status).toBe("hybrid");
    expect(g4TightOnH100.selectable).toBe(true);

    // 111 GiB and 170 GiB MoE/hybrid models -> hybrid (GPU+RAM offload) on G4 96G and H100 80G
    const moe111Vram = estimateColabOneVramMib(111 * 1024 ** 3, 8192);
    const moe111OnG4 = evaluateColabOneVramFit(moe111Vram, g4);
    expect(moe111OnG4.status).toBe("hybrid");
    expect(moe111OnG4.selectable).toBe(true);
    expect(moe111OnG4.requiresHybrid).toBe(true);
    expect(moe111OnG4.minGpu.id).toBe("OVER_96G");
    expect(moe111OnG4.label).toContain("混合装载");

    const moe170Vram = estimateColabOneVramMib(170 * 1024 ** 3, 8192);
    expect(evaluateColabOneVramFit(moe170Vram, g4).status).toBe("hybrid");
    expect(evaluateColabOneVramFit(moe170Vram, h100).status).toBe("hybrid");
    expect(evaluateColabOneVramFit(moe170Vram, all).status).toBe("unrestricted");
    expect(evaluateColabOneVramFit(moe170Vram, all).selectable).toBe(true);
  });

  it("increases VRAM estimate when context length increases", () => {
    const v8k = estimateColabOneVramMib(30 * 1024 ** 3, 8192);
    const v32k = estimateColabOneVramMib(30 * 1024 ** 3, 32768);
    const v64k = estimateColabOneVramMib(30 * 1024 ** 3, 65536);
    expect(v32k).toBeGreaterThan(v8k);
    expect(v64k).toBeGreaterThan(v32k);
  });
});

describe("Beginner-friendly quantization descriptions & curated version planner", () => {
  it("translates technical quantization names into clear Chinese labels", () => {
    expect(describeColabOneQuantization("UD-Q4_K_XL").title).toBe("主流均衡");
    expect(describeColabOneQuantization("Q8_0").title).toContain("高精度");
    expect(describeColabOneQuantization("BF16").title).toContain("原版");
    expect(describeColabOneQuantization("IQ2_XXS").title).toBe("极限压缩");
    expect(
      describeColabOneQuantization("GGUF", "Qwen3-27B-UD-Q4_K_XL-00001-of-00004.gguf").code,
    ).toBe("UD-Q4_K_XL");
  });

  it("recommends the best-fit version, keeps oversized valid versions selectable as hybrid, disables only broken files, and curates 3-4 beginner cards", () => {
    const files: ColabOneHubFile[] = [
      makeFile("Model-IQ2_XXS.gguf", 10, "IQ2_XXS"),
      makeFile("Model-Q3_K_M.gguf", 14, "Q3_K_M"),
      makeFile("Model-IQ4_XS.gguf", 16, "IQ4_XS"),
      makeFile("Model-Q4_K_M.gguf", 18, "Q4_K_M"),
      makeFile("Model-Q5_K_M.gguf", 22, "Q5_K_M"),
      makeFile("Model-Q6_K.gguf", 26, "Q6_K"),
      makeFile("Model-Q8_0.gguf", 33, "Q8_0"),
      makeFile("Model-BF16.gguf", 62, "BF16"),
      makeFile("Model-Broken.gguf", 15, "Q4_K_M", {
        supported: false,
        reason: "缺少部分分片",
      }),
    ];

    // On G4 (96 GB): all 8 supported files fit in pure VRAM, broken file stays disabled
    const g4Plan = planColabOneVersionOptions(
      files,
      resolveColabOneGpuTarget("G4"),
      8192,
    );
    expect(g4Plan.recommendedFilename).toBe("Model-Q4_K_M.gguf");
    expect(g4Plan.fullVramCount).toBe(8);
    expect(g4Plan.hybridCount).toBe(0);
    expect(g4Plan.unsupportedCount).toBe(1);
    expect(
      g4Plan.all.find((o) => o.file.filename === "Model-Broken.gguf")?.disabled,
    ).toBe(true);
    expect(g4Plan.curated.length).toBeLessThanOrEqual(4);
    expect(g4Plan.hasHidden).toBe(true);
    expect(g4Plan.curated.some((o) => o.recommended)).toBe(true);
    expect(
      g4Plan.curated.some(
        (o) => o.quant.bucket === "high" || o.quant.bucket === "lossless",
      ),
    ).toBe(true);

    // On L4 (24 GB): Q4_K_M fits tightly, Q5_K_M+ switch to hybrid (still selectable & NOT disabled)
    const l4Plan = planColabOneVersionOptions(
      files,
      resolveColabOneGpuTarget("L4"),
      8192,
    );
    const l4Bf16 = l4Plan.all.find((o) => o.file.filename === "Model-BF16.gguf");
    const l4Q8 = l4Plan.all.find((o) => o.file.filename === "Model-Q8_0.gguf");
    expect(l4Bf16?.disabled).toBe(false);
    expect(l4Bf16?.fit.requiresHybrid).toBe(true);
    expect(l4Q8?.disabled).toBe(false);
    expect(l4Q8?.fit.status).toBe("hybrid");
    expect(l4Plan.hybridCount).toBeGreaterThanOrEqual(3);
    // Comfortable IQ4_XS preferred over tight Q4_K_M and hybrid versions
    expect(l4Plan.recommendedFilename).toBe("Model-IQ4_XS.gguf");

    // 111 GiB / 170 GiB oversized MoE models on G4 (96 GB): all hybrid, still selectable & runnable!
    const megaFiles: ColabOneHubFile[] = [
      makeFile("Mega-Q4_K_XL.gguf", 111, "UD-Q4_K_XL"),
      makeFile("Mega-Q8_0.gguf", 170, "Q8_0"),
    ];
    const g4Mega = planColabOneVersionOptions(
      megaFiles,
      resolveColabOneGpuTarget("G4"),
      8192,
    );
    expect(g4Mega.fullVramCount).toBe(0);
    expect(g4Mega.hybridCount).toBe(2);
    expect(g4Mega.recommendedFilename).toBe("Mega-Q4_K_XL.gguf");
    expect(g4Mega.all.every((o) => !o.disabled)).toBe(true);

    const repoSummaryG4Mega = summarizeColabOneRepoFit(
      megaFiles,
      resolveColabOneGpuTarget("G4"),
      8192,
    );
    expect(repoSummaryG4Mega.status).toBe("hybrid");
    expect(repoSummaryG4Mega.runnable).toBe(true);
    expect(repoSummaryG4Mega.badgeLabel).toContain("混合装载");

    // On T4 (16 GB VRAM, ~75 GB usable disk): 68 GiB is heavy_hybrid (fits disk tightly) while 120 GiB exceeds T4 disk
    const hugeFiles: ColabOneHubFile[] = [
      makeFile("Big-Q4_K_M.gguf", 68, "Q4_K_M"),
      makeFile("Big-Q8_0.gguf", 120, "Q8_0"),
    ];
    const t4Huge = planColabOneVersionOptions(
      hugeFiles,
      resolveColabOneGpuTarget("T4"),
      8192,
    );
    expect(t4Huge.fullVramCount).toBe(0);
    expect(t4Huge.hybridCount).toBe(1);
    expect(t4Huge.diskExceedsVmCount).toBe(1);
    expect(t4Huge.recommendedFilename).toBe("Big-Q4_K_M.gguf");
    expect(t4Huge.suggestedGpuId).toBe("A100_80");
    expect(t4Huge.all.every((o) => !o.disabled)).toBe(true);

    const repoSummaryT4 = summarizeColabOneRepoFit(
      hugeFiles,
      resolveColabOneGpuTarget("T4"),
      8192,
    );
    expect(repoSummaryT4.status).toBe("heavy_hybrid");
    expect(repoSummaryT4.runnable).toBe(false);
    expect(repoSummaryT4.badgeLabel).toContain("超大容量");

    const repoSummaryG4 = summarizeColabOneRepoFit(
      hugeFiles,
      resolveColabOneGpuTarget("G4"),
      8192,
    );
    expect(repoSummaryG4.runnable).toBe(true);
    expect(repoSummaryG4.badgeLabel).toContain("适配 G4");
  });
});

describe("Runtime parameter presets & effect descriptions", () => {
  it("matches built-in scenario presets and generates human-readable effect summaries", () => {
    expect(matchColabOneParamPreset(8192, 0.7)?.id).toBe("balanced");
    expect(matchColabOneParamPreset(16384, 0.2)?.id).toBe("coding");
    expect(matchColabOneParamPreset(32768, 0.4)?.id).toBe("long_context");
    expect(matchColabOneParamPreset(8192, 1.1)?.id).toBe("creative");
    expect(matchColabOneParamPreset(65536, 0.5)).toBeNull();
    expect(COLABONE_PARAM_PRESETS.some((p) => p.badge === "推荐")).toBe(true);

    const coding = describeColabOneRuntimeParams(16384, 0.2);
    expect(coding.presetTitle).toContain("精准编程");
    expect(coding.contextEffect).toContain("1.2 万");
    expect(coding.temperatureEffect).toContain("严谨");

    const custom = describeColabOneRuntimeParams(65536, 1.5);
    expect(custom.presetTitle).toBe("自定义档位");
    expect(custom.contextK).toBe(64);
    expect(custom.temperatureEffect).toContain("随机");

    const ultra = describeColabOneRuntimeParams(131072, 0.4);
    expect(ultra.contextK).toBe(128);
    expect(ultra.contextEffect).toContain("9.6 万");
  });
});

describe("ColabOne remote disk fit evaluation", () => {
  const GIB = 1024 ** 3;
  const MIB = 1024 ** 2;
  const makeModel = (overrides: Partial<ModelSpec> = {}): ModelSpec => ({
    id: "qwen-flash-q8",
    name: "Qwen3.8-Flash-Next-GGUF",
    description: "Q8_0",
    downloaded: false,
    loaded: false,
    size: Math.round(175.3 * GIB),
    min_vram_mib: 180000,
    files: [{ repo: "org/repo", path: "model-00001-of-00006.gguf" }],
    ...overrides,
  });

  const makeRuntime = (
    overrides: Partial<RuntimeStatus> = {},
  ): RuntimeStatus => ({
    state: "ready",
    disk_free: Math.round(67.9 * GIB),
    resources: {
      disk: {
        total_bytes: Math.round(235.7 * GIB),
        used_bytes: Math.round(167.8 * GIB),
        free_bytes: Math.round(67.9 * GIB),
        percent: 71.2,
      },
    },
    tasks: [],
    ...overrides,
  });

  it("does not warn during active download when free disk covers remaining bytes", () => {
    const model = makeModel();
    const runtime = makeRuntime({
      tasks: [
        {
          id: "dl-1",
          kind: "download",
          model_id: model.id,
          state: "running",
          progress: 0.96,
          message: "shard 6",
          error: "",
          download: {
            downloaded_bytes: Math.round(167.6 * GIB),
            total_bytes: Math.round(175.3 * GIB),
            speed_bps: 229.9 * MIB,
            eta_seconds: 35,
            elapsed_seconds: 800,
            resumed_bytes: 0,
            threads: 32,
            connections: 32,
            phase: "downloading",
            filename: "shard-6.gguf",
            file_index: 6,
            file_count: 6,
          },
        },
      ],
    });

    const fit = evaluateColabOneModelDiskFit(model, runtime);
    expect(fit.insufficient).toBe(false);
    expect(fit.warning).toBe("");
    expect(fit.cachedBytes).toBe(Math.round(167.6 * GIB));
    expect(fit.remainingBytes).toBe(
      Math.round(175.3 * GIB) - Math.round(167.6 * GIB),
    );
  });

  it("warns against remaining bytes (with cached amount) when active download truly exceeds free disk", () => {
    const model = makeModel();
    const runtime = makeRuntime({
      disk_free: Math.round(5.0 * GIB),
      resources: {
        disk: {
          total_bytes: Math.round(235.7 * GIB),
          used_bytes: Math.round(230.7 * GIB),
          free_bytes: Math.round(5.0 * GIB),
          percent: 97.9,
        },
      },
      tasks: [
        {
          id: "dl-2",
          kind: "download",
          model_id: model.id,
          state: "running",
          progress: 0.96,
          message: "shard 6",
          error: "",
          download: {
            downloaded_bytes: Math.round(167.6 * GIB),
            total_bytes: Math.round(175.3 * GIB),
            speed_bps: 200 * MIB,
            eta_seconds: 40,
            elapsed_seconds: 800,
            resumed_bytes: 0,
            threads: 32,
            connections: 32,
            phase: "downloading",
            filename: "shard-6.gguf",
            file_index: 6,
            file_count: 6,
          },
        },
      ],
    });

    const fit = evaluateColabOneModelDiskFit(model, runtime);
    expect(fit.insufficient).toBe(true);
    expect(fit.warning).toContain("5.0 GB");
    expect(fit.warning).toContain("7.7 GB");
    expect(fit.warning).toContain("167.6 GB");
    expect(fit.warning).not.toContain("175.3 GB");
  });

  it("accounts for server-reported cached_bytes after cancellation or during resume verification", () => {
    const partialModel = makeModel({ cached_bytes: Math.round(167.6 * GIB) });

    // Paused / cancelled with 67.9 GB free vs 7.7 GB remaining -> fits
    const okFit = evaluateColabOneModelDiskFit(partialModel, makeRuntime());
    expect(okFit.insufficient).toBe(false);
    expect(okFit.warning).toBe("");

    // Resuming while server is still hashing shard 1 (task downloaded_bytes=0) -> no false flicker
    const resumingRuntime = makeRuntime({
      tasks: [
        {
          id: "dl-resume",
          kind: "download",
          model_id: partialModel.id,
          state: "running",
          progress: 0,
          message: "verifying shard 1",
          error: "",
          download: {
            downloaded_bytes: 0,
            total_bytes: Math.round(175.3 * GIB),
            speed_bps: 0,
            eta_seconds: null,
            elapsed_seconds: 2,
            resumed_bytes: 0,
            threads: 32,
            connections: 0,
            phase: "connecting",
            filename: "shard-1.gguf",
            file_index: 1,
            file_count: 6,
          },
        },
      ],
    });
    expect(evaluateColabOneModelDiskFit(partialModel, resumingRuntime).insufficient).toBe(false);

    // Paused / cancelled with only 4.0 GB free vs 7.7 GB remaining -> warns against remaining
    const lowFit = evaluateColabOneModelDiskFit(
      partialModel,
      makeRuntime({
        disk_free: Math.round(4.0 * GIB),
        resources: {
          disk: {
            total_bytes: Math.round(235.7 * GIB),
            used_bytes: Math.round(231.7 * GIB),
            free_bytes: Math.round(4.0 * GIB),
            percent: 98.3,
          },
        },
      }),
    );
    expect(lowFit.insufficient).toBe(true);
    expect(lowFit.warning).toContain("4.0 GB");
    expect(lowFit.warning).toContain("7.7 GB");
    expect(lowFit.warning).toContain("167.6 GB");
  });

  it("warns against full model size for untouched models and handles zero free disk, completion, and rounding", () => {
    const untouched = makeModel();
    const fit = evaluateColabOneModelDiskFit(untouched, makeRuntime());
    expect(fit.insufficient).toBe(true);
    expect(fit.warning).toContain("67.9 GB");
    expect(fit.warning).toContain("175.3 GB");

    // Genuinely 0 free bytes on a mounted volume (total_bytes > 0) still warns
    const fullDiskFit = evaluateColabOneModelDiskFit(
      untouched,
      makeRuntime({
        disk_free: 0,
        resources: {
          disk: {
            total_bytes: Math.round(235.7 * GIB),
            used_bytes: Math.round(235.7 * GIB),
            free_bytes: 0,
            percent: 100,
          },
        },
      }),
    );
    expect(fullDiskFit.insufficient).toBe(true);
    expect(fullDiskFit.warning).toContain("0 MB");

    // Telemetry failure (total_bytes=0, free_bytes=0) does not falsely warn
    const unknownDiskFit = evaluateColabOneModelDiskFit(
      untouched,
      makeRuntime({
        disk_free: 0,
        resources: {
          disk: { total_bytes: 0, used_bytes: 0, free_bytes: 0, percent: 0 },
        },
      }),
    );
    expect(unknownDiskFit.insufficient).toBe(false);

    // Downloaded, loaded, loading, verifying (remaining=0), or stopped never warn
    expect(
      evaluateColabOneModelDiskFit(makeModel({ downloaded: true }), makeRuntime())
        .insufficient,
    ).toBe(false);
    expect(
      evaluateColabOneModelDiskFit(
        untouched,
        makeRuntime({ loaded_model: untouched.id }),
      ).insufficient,
    ).toBe(false);
    expect(
      evaluateColabOneModelDiskFit(
        untouched,
        makeRuntime({ state: "stopped" }),
      ).insufficient,
    ).toBe(false);
    expect(
      evaluateColabOneModelDiskFit(
        untouched,
        makeRuntime({
          disk_free: 100 * MIB,
          resources: undefined,
          tasks: [
            {
              id: "dl-verify",
              kind: "download",
              model_id: untouched.id,
              state: "running",
              progress: 1,
              message: "verifying",
              error: "",
              download: {
                downloaded_bytes: untouched.size,
                total_bytes: untouched.size,
                speed_bps: 0,
                eta_seconds: 0,
                elapsed_seconds: 900,
                resumed_bytes: 0,
                threads: 32,
                connections: 0,
                phase: "verifying",
                filename: "shard-6.gguf",
                file_index: 6,
                file_count: 6,
              },
            },
          ],
        }),
      ).insufficient,
    ).toBe(false);

    // Disambiguates values that would round identically at 1 decimal place
    const tightFit = evaluateColabOneModelDiskFit(
      makeModel({ size: Math.round(7.74 * GIB), cached_bytes: 0 }),
      makeRuntime({
        disk_free: Math.round(7.71 * GIB),
        resources: undefined,
      }),
    );
    expect(tightFit.insufficient).toBe(true);
    expect(tightFit.warning).toContain("7.71 GB");
    expect(tightFit.warning).toContain("7.74 GB");

    // Sub-1MiB cache does not display misleading "(已缓存 0 MB)"
    const tinyCacheFit = evaluateColabOneModelDiskFit(
      makeModel({ size: 20 * GIB, cached_bytes: 256 * 1024 }),
      makeRuntime({ disk_free: 10 * GIB, resources: undefined }),
    );
    expect(tinyCacheFit.insufficient).toBe(true);
    expect(tinyCacheFit.warning).not.toContain("0 MB");
  });
});

describe("Copy & VRAM edge-case consistency", () => {
  it("handles near-limit hybrid VRAM without contradictory overflow numbers", () => {
    const g4 = resolveColabOneGpuTarget("G4");
    const nearLimitMib = Math.round(94.7 * 1024); // ratio ~0.986 > 0.98, but < 96 GB
    const fit = evaluateColabOneVramFit(nearLimitMib, g4);
    expect(fit.status).toBe("hybrid");
    expect(fit.requiresHybrid).toBe(true);
    expect(fit.overflowGb).toBe(0);
    expect(fit.detail).not.toContain("0.5 GB");
    expect(fit.detail).toContain("接近");
  });

  it("evaluates ComfyUI-only image/video models without LLM KV-cache or --fit on hybrid copy", () => {
    const l4 = resolveColabOneGpuTarget("L4");
    const vramZeroCtx = estimateColabOneVramMib(25 * 1024 ** 3, 0);
    const vram8kCtx = estimateColabOneVramMib(25 * 1024 ** 3, 8192);
    expect(vramZeroCtx).toBeLessThan(vram8kCtx);

    const comfyFiles = [makeFile("Wan2.2-Q8_0.gguf", 25, "Q8_0")];
    const plan = planColabOneVersionOptions(comfyFiles, l4, 8192, "", undefined, true);
    expect(plan.all[0]!.fit.requiresHybrid).toBe(false);
    expect(plan.all[0]!.fit.label).toBe("超出显存");
    expect(plan.all[0]!.fit.detail).not.toContain("--fit on");

    const summary = summarizeColabOneRepoFit(comfyFiles, l4, 8192, true);
    expect(summary.runnable).toBe(false);
    expect(summary.badgeLabel).not.toContain("混合装载");
    expect(summary.badgeLabel).toContain("超出");
  });

  it("never suggests the same GPU tier for a live GPU with slightly lower reported VRAM", () => {
    const liveT4 = resolveColabOneGpuTarget("CURRENT", {
      state: "ready",
      gpu: { name: "Tesla T4", total_mib: 15360, used_mib: 0 },
    });
    // Weight ~13.3 GiB -> est ~15.4 GiB: exceeds 15360 * 0.98 (hybrid on live 15 GiB T4),
    // and fits static 16 GiB * 0.98 (minGpu.id === "T4"). Must NOT suggest "Switch to T4 16G".
    const planOnLiveT4 = planColabOneVersionOptions(
      [makeFile("Model-Q4.gguf", 13.3, "Q4_K_M")],
      liveT4,
      8192,
    );
    expect(planOnLiveT4.hybridCount).toBe(1);
    expect(planOnLiveT4.suggestedGpuId).toBeNull();
  });

  it("classifies UD-IQ4_XS as compact 4-bit instead of generic standard version", () => {
    expect(describeColabOneQuantization("UD-IQ4_XS").title).toBe("轻量 4-bit");
  });

  it("evaluates Colab VM disk capacity for static tiers, live telemetry, partial cache, and repo-level disk overflow", () => {
    const t4 = resolveColabOneGpuTarget("T4");
    const l4 = resolveColabOneGpuTarget("L4");
    const g4 = resolveColabOneGpuTarget("G4");
    expect(t4.diskFreeGb).toBe(75);
    expect(t4.diskTotalGb).toBe(112);
    expect(l4.diskFreeGb).toBe(195);
    expect(g4.diskFreeGb).toBe(195);
    expect(g4.diskTotalGb).toBe(235);

    // 175.3 GiB model: fits tightly on clean G4 (195 GB free), exceeds T4 (75 GB free)
    const q8Bytes = Math.round(175.3 * 1024 ** 3);
    const g4Disk = evaluateColabOneTargetDiskFit(q8Bytes, g4);
    expect(g4Disk.status).toBe("tight");
    expect(g4Disk.insufficient).toBe(false);
    expect(g4Disk.label).toBe("磁盘偏紧");

    const t4Disk = evaluateColabOneTargetDiskFit(q8Bytes, t4);
    expect(t4Disk.status).toBe("exceeds_vm");
    expect(t4Disk.insufficient).toBe(true);
    expect(t4Disk.exceedsVm).toBe(true);
    expect(t4Disk.label).toBe("超出磁盘");

    // Live G4 with 156.2 GB free out of 235.7 GB total:
    // untouched 175.3 GiB -> insufficient_live (fits after deleting cached models),
    // partially cached 79.3 GiB -> remaining 96 GiB fits cleanly!
    const liveG4 = resolveColabOneGpuTarget("CURRENT", {
      state: "ready",
      gpu: { name: "NVIDIA RTX PRO 6000", total_mib: 97887, used_mib: 1024 },
      resources: {
        disk: {
          total_bytes: Math.round(235.7 * 1024 ** 3),
          used_bytes: Math.round(79.5 * 1024 ** 3),
          free_bytes: Math.round(156.2 * 1024 ** 3),
          percent: 33.7,
        },
      },
    });
    expect(liveG4.isLiveDisk).toBe(true);
    expect(liveG4.diskFreeGb).toBe(156.2);
    expect(liveG4.subtitle).toContain("156.2 GB");

    const liveUntouched = evaluateColabOneTargetDiskFit(q8Bytes, liveG4);
    expect(liveUntouched.status).toBe("insufficient_live");
    expect(liveUntouched.insufficient).toBe(true);
    expect(liveUntouched.exceedsVm).toBe(false);

    const livePartial = evaluateColabOneTargetDiskFit(q8Bytes, liveG4, {
      cachedBytes: Math.round(79.3 * 1024 ** 3),
    });
    expect(livePartial.status).toBe("ok");
    expect(livePartial.insufficient).toBe(false);

    const liveDownloaded = evaluateColabOneTargetDiskFit(q8Bytes, liveG4, {
      downloaded: true,
    });
    expect(liveDownloaded.status).toBe("cached");
    expect(liveDownloaded.insufficient).toBe(false);

    // Downloaded state on live VM must NOT mask disk overflow when previewing a static GPU tier (e.g. T4)
    const staticT4WithLiveState = evaluateColabOneTargetDiskFit(q8Bytes, t4, {
      downloaded: true,
    });
    expect(staticT4WithLiveState.status).toBe("exceeds_vm");
    expect(staticT4WithLiveState.exceedsVm).toBe(true);

    // 250 GiB model exceeds even live G4 total disk capacity -> exceeds_vm
    const huge250 = evaluateColabOneTargetDiskFit(250 * 1024 ** 3, liveG4);
    expect(huge250.status).toBe("exceeds_vm");
    expect(huge250.exceedsVm).toBe(true);

    // Repo where all versions exceed T4 disk (90 GiB & 175 GiB) -> disk_overflow, runnable=false,
    // no contradictory "recommended" badge, and suggests larger-disk GPU
    const bigFiles = [
      makeFile("Huge-Q4_K_M.gguf", 90, "Q4_K_M"),
      makeFile("Huge-Q8_0.gguf", 175, "Q8_0"),
    ];
    const planOnT4 = planColabOneVersionOptions(bigFiles, t4, 8192);
    expect(planOnT4.diskFittingCount).toBe(0);
    expect(planOnT4.diskExceedsVmCount).toBe(2);
    expect(planOnT4.suggestedDiskGpuId).not.toBeNull();
    expect(planOnT4.all.every((o) => !o.recommended)).toBe(true);
    expect(planOnT4.recommendedFilename).toBe("Huge-Q4_K_M.gguf");

    // hiddenHybridCount excludes versions that exceed VM disk
    const mixedT4Files = [
      makeFile("M-IQ2_XXS.gguf", 4, "IQ2_XXS"),
      makeFile("M-Q3_K_M.gguf", 6, "Q3_K_M"),
      makeFile("M-Q4_K_M.gguf", 8, "Q4_K_M"),
      makeFile("M-Q5_K_M.gguf", 10, "Q5_K_M"),
      makeFile("M-Q8_0.gguf", 12, "Q8_0"),
      makeFile("M-Huge.gguf", 120, "BF16"),
    ];
    const mixedPlanOnT4 = planColabOneVersionOptions(mixedT4Files, t4, 8192);
    expect(mixedPlanOnT4.diskExceedsVmCount).toBe(1);
    expect(mixedPlanOnT4.hiddenHybridCount).toBe(0);

    const summaryOnT4 = summarizeColabOneRepoFit(bigFiles, t4, 8192);
    expect(summaryOnT4.status).toBe("disk_overflow");
    expect(summaryOnT4.runnable).toBe(false);
    expect(summaryOnT4.diskExceedsVm).toBe(true);
    expect(summaryOnT4.badgeLabel).toContain("磁盘");

    // Repo where all versions exceed even G4 disk (220 GiB) -> disk_overflow on G4 too
    const giantSummary = summarizeColabOneRepoFit(
      [makeFile("Giant-BF16.gguf", 220, "BF16")],
      g4,
      8192,
    );
    expect(giantSummary.status).toBe("disk_overflow");
    expect(giantSummary.runnable).toBe(false);
  });

  it("exposes popular MiniMax H3 and active video/image families in community filter chips", () => {
    const videoIds = COLABONE_HUB_FAMILIES_BY_MODALITY.video.map((f) => f.id);
    expect(videoIds).toEqual([
      "",
      "MiniMax-H3",
      "Wan",
      "LTX",
      "HunyuanVideo",
      "SeedVR2",
      "SkyReels",
      "SCAIL",
    ]);
    expect(
      COLABONE_HUB_FAMILIES_BY_MODALITY.video.find((f) => f.id === "MiniMax-H3")
        ?.label,
    ).toBe("MiniMax H3");

    const imageIds = COLABONE_HUB_FAMILIES_BY_MODALITY.image.map((f) => f.id);
    expect(imageIds).toContain("MiniMax-H3");
    expect(imageIds).toContain("Qwen-Image");
    expect(imageIds).toContain("Z-Image");
    expect(imageIds).toContain("FLUX");
    expect(imageIds).toContain("MOSS-Image");
  });

  it("formats live B200, H200, V100, and G4 GPU display names without mislabeling B200 or H200 as G4", () => {
    const b200 = resolveColabOneGpuTarget(
      "CURRENT",
      {
        state: "ready",
        gpu: { name: "NVIDIA B200", total_mib: 184320, used_mib: 1024 },
      },
      "G4",
    );
    expect(b200.shortLabel).toBe("B200 180G");
    expect(b200.shortLabel).not.toContain("G4");

    const h200 = resolveColabOneGpuTarget(
      "CURRENT",
      {
        state: "ready",
        gpu: { name: "NVIDIA H200 SXM", total_mib: 144384, used_mib: 1024 },
      },
      "G4",
    );
    expect(h200.shortLabel).toBe("H200 141G");
    expect(h200.shortLabel).not.toContain("G4");

    const v100 = resolveColabOneGpuTarget(
      "CURRENT",
      {
        state: "ready",
        gpu: { name: "Tesla V100-SXM2-16GB", total_mib: 16384, used_mib: 512 },
      },
      "T4",
    );
    expect(v100.shortLabel).toBe("V100 16G");

    const g4 = resolveColabOneGpuTarget(
      "CURRENT",
      {
        state: "ready",
        gpu: {
          name: "NVIDIA RTX PRO 6000 Blackwell",
          total_mib: 98304,
          used_mib: 1024,
        },
      },
      "G4",
    );
    expect(g4.shortLabel).toBe("G4 96G");
  });

  it("avoids hardcoded 96G overflow copy when evaluating VRAM overflow on a live B200 180 GB GPU", () => {
    const liveB200 = resolveColabOneGpuTarget(
      "CURRENT",
      {
        state: "ready",
        gpu: { name: "NVIDIA B200", total_mib: 184320, used_mib: 1024 },
        resources: {
          memory: {
            total_bytes: 200 * 1024 ** 3,
            used_bytes: 10 * 1024 ** 3,
            available_bytes: 190 * 1024 ** 3,
            percent: 5,
          },
        },
      },
      "G4",
    );
    expect(liveB200.vramGb).toBe(180);

    // 200 GiB model exceeds live B200 180 GB VRAM in both comfyOnly and LLM heavy-hybrid modes
    const hugeComfyFit = evaluateColabOneVramFit(200 * 1024, liveB200, true);
    expect(hugeComfyFit.selectable).toBe(false);
    expect(hugeComfyFit.detail).not.toMatch(/96\s*G/i);
    expect(hugeComfyFit.detail).toContain("180");

    const hugeLlmFit = evaluateColabOneVramFit(400 * 1024, liveB200, false);
    expect(hugeLlmFit.status).toBe("heavy_hybrid");
    expect(hugeLlmFit.requiresHybrid).toBe(true);
    expect(hugeLlmFit.detail).not.toMatch(/96\s*G/i);
    expect(hugeLlmFit.detail).toContain("180");
  });

  it("excludes VRAM-overflowing comfyOnly files from fullVramPool, clears recommended when unselectable, and suggests a larger GPU", () => {
    const t4 = resolveColabOneGpuTarget("T4", undefined, "T4");
    const comfyOverflowFiles = [
      makeFile("Wan2_1_14B_fp8.safetensors", 17, "FP8"),
      makeFile("Wan2_1_14B_bf16.safetensors", 28, "BF16"),
    ];

    const plan = planColabOneVersionOptions(
      comfyOverflowFiles,
      t4,
      4096,
      "",
      undefined,
      true,
    );
    expect(plan.diskFittingCount).toBe(2);
    expect(plan.fullVramCount).toBe(0);
    expect(plan.hybridCount).toBe(0);
    expect(plan.all.every((o) => !o.fit.selectable)).toBe(true);
    expect(plan.all.every((o) => !o.recommended)).toBe(true);
    expect(plan.suggestedGpuId).not.toBeNull();
    expect(plan.suggestedGpuId).not.toBe("T4");
  });
});
