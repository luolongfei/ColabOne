import { describe, expect, it } from "vitest";
import {
  COLABONE_PARAM_PRESETS,
  buildColabOneGpuTargetOptions,
  describeColabOneQuantization,
  describeColabOneRuntimeParams,
  estimateColabOneVramMib,
  evaluateColabOneVramFit,
  matchColabOneParamPreset,
  planColabOneVersionOptions,
  resolveColabOneDefaultGpuTarget,
  resolveColabOneGpuTarget,
  summarizeColabOneRepoFit,
  type ColabOneHubFile,
} from "./modelCompatibility";

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
    expect(describeColabOneQuantization("Q8_0").title).toContain("高画质");
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

    // On T4 (16 GB): huge files are heavy_hybrid (still selectable, suggests A100/H100 for full VRAM, runnable=false for Fits Only toggle)
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
    expect(t4Huge.hybridCount).toBe(2);
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
  });
});
