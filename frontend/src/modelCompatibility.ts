import { t, tb } from "./i18n";
import {
  COLABONE_DEFAULT_GPU,
  type ModelSpec,
  type RuntimeStatus,
} from "./types";

export type ColabOneGpuTargetId =
  | "CURRENT"
  | "G4"
  | "H100"
  | "A100_80"
  | "A100_40"
  | "L4"
  | "T4"
  | "ALL";

export interface ColabOneGpuTarget {
  id: ColabOneGpuTargetId;
  shortLabel: string;
  label: string;
  subtitle: string;
  vramGb: number;
  vramMib: number;
  /** Typical paired VM host RAM (GB) available for hybrid GPU+RAM offloading (--fit on). */
  hostRamGb: number;
  /** Typical (or live measured) usable free disk capacity (GB) available for model weights. */
  diskFreeGb: number;
  /** Typical (or live measured) total local disk size (GB) of the Colab VM. */
  diskTotalGb: number;
  /** True when diskFreeGb comes from live runtime telemetry rather than static tier estimate. */
  isLiveDisk?: boolean;
  isLive?: boolean;
}

export type ColabOneHubModality = "text" | "image" | "video" | "all";

export interface ColabOneModalityOption {
  id: ColabOneHubModality;
  label: string;
  shortLabel: string;
  comfyOnly?: boolean;
}

export interface ColabOneFamilyOption {
  id: string;
  label: string;
}

export interface ColabOneHubFile {
  filename: string;
  size: number;
  shards: number;
  quantization: string;
  supported: boolean;
  reason: string;
}

export type ColabOneVramFitStatus =
  | "comfortable"
  | "tight"
  | "hybrid"
  | "heavy_hybrid"
  | "unrestricted";

export interface ColabOneQuantInfo {
  code: string;
  title: string;
  desc: string;
  bucket: "lossless" | "high" | "balanced" | "compact" | "extreme";
  /** Higher means closer to full precision (used for ordering). */
  qualityTier: number;
  /** Preference score for picking the single best default when it fits comfortably. */
  defaultScore: number;
}

export interface ColabOneMinGpuHint {
  id: Exclude<ColabOneGpuTargetId, "CURRENT" | "ALL"> | "OVER_96G";
  label: string;
}

export interface ColabOneVramFit {
  status: ColabOneVramFitStatus;
  ratio: number;
  estimatedVramMib: number;
  estimatedVramGb: number;
  overflowGb: number;
  label: string;
  detail: string;
  selectable: boolean;
  requiresHybrid: boolean;
  minGpu: ColabOneMinGpuHint;
}

export type ColabOneDiskFitStatus =
  | "ok"
  | "tight"
  | "insufficient_live"
  | "exceeds_vm"
  | "cached"
  | "unrestricted";

export interface ColabOneFileDiskState {
  downloaded?: boolean;
  cachedBytes?: number;
}

export interface ColabOneTargetDiskFit {
  status: ColabOneDiskFitStatus;
  requiredGb: number;
  remainingGb: number;
  availableGb: number;
  totalGb: number;
  isLive: boolean;
  ratio: number;
  insufficient: boolean;
  exceedsVm: boolean;
  label: string;
  detail: string;
}

export interface ColabOneVersionOption {
  file: ColabOneHubFile;
  quant: ColabOneQuantInfo;
  fit: ColabOneVramFit;
  disk: ColabOneTargetDiskFit;
  recommended: boolean;
  disabled: boolean;
  disabledReason: string;
}

export interface ColabOneVersionPlan {
  all: ColabOneVersionOption[];
  curated: ColabOneVersionOption[];
  recommendedFilename: string;
  fullVramCount: number;
  hybridCount: number;
  diskFittingCount: number;
  diskInsufficientCount: number;
  diskExceedsVmCount: number;
  unsupportedCount: number;
  totalCount: number;
  hasHidden: boolean;
  hiddenHybridCount: number;
  suggestedGpuId: Exclude<ColabOneGpuTargetId, "CURRENT" | "ALL"> | null;
  suggestedGpuLabel: string;
  suggestedDiskGpuId: Exclude<ColabOneGpuTargetId, "CURRENT" | "ALL"> | null;
  suggestedDiskGpuLabel: string;
  suggestedDiskAvailGb: number;
}

export interface ColabOneRepoFitSummary {
  status:
    | "compatible"
    | "tight"
    | "hybrid"
    | "heavy_hybrid"
    | "disk_overflow"
    | "unrestricted"
    | "unknown";
  fullVramCount: number;
  hybridCount: number;
  diskFittingCount: number;
  diskExceedsVm: boolean;
  totalSupported: number;
  minEstimatedGb: number;
  maxEstimatedGb: number;
  recommendedQuant: string;
  badgeLabel: string;
  minGpuHint: string;
  runnable: boolean;
}

export interface ColabOneParamPreset {
  id: "balanced" | "coding" | "long_context" | "creative";
  label: string;
  badge?: string;
  context_length: number;
  temperature: number;
  summary: string;
  vramHint: string;
}

export const COLABONE_STATIC_GPU_TARGETS: readonly ColabOneGpuTarget[] = [
  {
    id: "G4",
    shortLabel: "G4 96G",
    label: "G4 · 96 GB",
    get subtitle() {
      return `${t("models.compatGpuG4Sub")} · ${t("models.compatGpuDiskApprox", { gb: 195 })}`;
    },
    vramGb: 96,
    vramMib: 96 * 1024,
    hostRamGb: 170,
    diskFreeGb: 195,
    diskTotalGb: 235,
  },
  {
    id: "H100",
    shortLabel: "H100 80G",
    label: "H100 · 80 GB",
    get subtitle() {
      return `${t("models.compatGpuH100Sub")} · ${t("models.compatGpuDiskApprox", { gb: 195 })}`;
    },
    vramGb: 80,
    vramMib: 80 * 1024,
    hostRamGb: 165,
    diskFreeGb: 195,
    diskTotalGb: 235,
  },
  {
    id: "A100_80",
    shortLabel: "A100 80G",
    label: "A100 · 80 GB",
    get subtitle() {
      return `${t("models.compatGpuA10080Sub")} · ${t("models.compatGpuDiskApprox", { gb: 195 })}`;
    },
    vramGb: 80,
    vramMib: 80 * 1024,
    hostRamGb: 165,
    diskFreeGb: 195,
    diskTotalGb: 235,
  },
  {
    id: "A100_40",
    shortLabel: "A100 40G",
    label: "A100 · 40 GB",
    get subtitle() {
      return `${t("models.compatGpuA10040Sub")} · ${t("models.compatGpuDiskApprox", { gb: 195 })}`;
    },
    vramGb: 40,
    vramMib: 40 * 1024,
    hostRamGb: 83,
    diskFreeGb: 195,
    diskTotalGb: 235,
  },
  {
    id: "L4",
    shortLabel: "L4 24G",
    label: "L4 · 24 GB",
    get subtitle() {
      return `${t("models.compatGpuL4Sub")} · ${t("models.compatGpuDiskApprox", { gb: 195 })}`;
    },
    vramGb: 24,
    vramMib: 24 * 1024,
    hostRamGb: 53,
    diskFreeGb: 195,
    diskTotalGb: 235,
  },
  {
    id: "T4",
    shortLabel: "T4 16G",
    label: "T4 · 16 GB",
    get subtitle() {
      return `${t("models.compatGpuT4Sub")} · ${t("models.compatGpuDiskApprox", { gb: 75 })}`;
    },
    vramGb: 16,
    vramMib: 16 * 1024,
    hostRamGb: 13,
    diskFreeGb: 75,
    diskTotalGb: 112,
  },
  {
    id: "ALL",
    get shortLabel() {
      return t("models.compatGpuAllShort");
    },
    get label() {
      return t("models.compatGpuAllLabel");
    },
    get subtitle() {
      return t("models.compatGpuAllSub");
    },
    vramGb: 0,
    vramMib: 0,
    hostRamGb: 0,
    diskFreeGb: 0,
    diskTotalGb: 0,
  },
];

export const COLABONE_HUB_MODALITIES: readonly ColabOneModalityOption[] = [
  {
    id: "text",
    get label() {
      return t("models.compatModalityText");
    },
    get shortLabel() {
      return t("models.compatModalityTextShort");
    },
  },
  {
    id: "image",
    get label() {
      return t("models.compatModalityImage");
    },
    get shortLabel() {
      return t("models.compatModalityImageShort");
    },
    comfyOnly: true,
  },
  {
    id: "video",
    get label() {
      return t("models.compatModalityVideo");
    },
    get shortLabel() {
      return t("models.compatModalityVideoShort");
    },
    comfyOnly: true,
  },
  {
    id: "all",
    get label() {
      return t("common.all");
    },
    get shortLabel() {
      return t("common.all");
    },
  },
];

const TEXT_FAMILIES: readonly ColabOneFamilyOption[] = [
  {
    id: "",
    get label() {
      return t("common.all");
    },
  },
  {
    id: "Qwen",
    get label() {
      return t("models.compatFamilyQwen");
    },
  },
  { id: "DeepSeek", label: "DeepSeek" },
  {
    id: "GLM",
    get label() {
      return t("models.compatFamilyGlm");
    },
  },
  { id: "Gemma", label: "Gemma" },
  { id: "Llama", label: "Llama" },
  { id: "Mistral", label: "Mistral" },
];

export const COLABONE_HUB_FAMILIES_BY_MODALITY: Record<
  ColabOneHubModality,
  readonly ColabOneFamilyOption[]
> = {
  text: TEXT_FAMILIES,
  all: TEXT_FAMILIES,
  image: [
    {
      id: "",
      get label() {
        return t("common.all");
      },
    },
    { id: "Qwen-Image", label: "Qwen-Image" },
    { id: "Z-Image", label: "Z-Image" },
    { id: "MiniMax-H3", label: "MiniMax H3" },
    { id: "FLUX", label: "FLUX" },
    { id: "MOSS-Image", label: "MOSS-Image" },
    { id: "Chroma", label: "Chroma" },
    { id: "HiDream", label: "HiDream" },
    { id: "SD3", label: "SD3.5 / SDXL" },
  ],
  video: [
    {
      id: "",
      get label() {
        return t("common.all");
      },
    },
    { id: "MiniMax-H3", label: "MiniMax H3" },
    {
      id: "Wan",
      get label() {
        return t("models.compatFamilyWan");
      },
    },
    { id: "LTX", label: "LTX-Video" },
    {
      id: "HunyuanVideo",
      get label() {
        return t("models.compatFamilyHunyuan");
      },
    },
    { id: "SeedVR2", label: "SeedVR2" },
    { id: "SkyReels", label: "SkyReels" },
    { id: "SCAIL", label: "SCAIL" },
  ],
};

export const COLABONE_CONTEXT_OPTIONS = [
  {
    value: 8192,
    label: "8K",
    get approxChars() {
      return t("models.compatCtx8kChars");
    },
    get hint() {
      return t("models.compatCtx8kHint");
    },
  },
  {
    value: 16384,
    label: "16K",
    get approxChars() {
      return t("models.compatCtx16kChars");
    },
    get hint() {
      return t("models.compatCtx16kHint");
    },
  },
  {
    value: 32768,
    label: "32K",
    get approxChars() {
      return t("models.compatCtx32kChars");
    },
    get hint() {
      return t("models.compatCtx32kHint");
    },
  },
  {
    value: 65536,
    label: "64K",
    get approxChars() {
      return t("models.compatCtx64kChars");
    },
    get hint() {
      return t("models.compatCtx64kHint");
    },
  },
  {
    value: 131072,
    label: "128K",
    get approxChars() {
      return t("models.compatCtx128kChars");
    },
    get hint() {
      return t("models.compatCtx128kHint");
    },
  },
] as const;

export const COLABONE_PARAM_PRESETS: readonly ColabOneParamPreset[] = [
  {
    id: "balanced",
    get label() {
      return t("models.compatPresetBalanced");
    },
    get badge() {
      return t("common.recommended");
    },
    context_length: 8192,
    temperature: 0.7,
    get summary() {
      return t("models.compatPresetBalancedSum");
    },
    get vramHint() {
      return t("models.compatPresetBalancedVram");
    },
  },
  {
    id: "coding",
    get label() {
      return t("models.compatPresetCoding");
    },
    context_length: 16384,
    temperature: 0.2,
    get summary() {
      return t("models.compatPresetCodingSum");
    },
    get vramHint() {
      return t("models.compatPresetCodingVram");
    },
  },
  {
    id: "long_context",
    get label() {
      return t("models.compatPresetLongCtx");
    },
    context_length: 32768,
    temperature: 0.4,
    get summary() {
      return t("models.compatPresetLongCtxSum");
    },
    get vramHint() {
      return t("models.compatPresetLongCtxVram");
    },
  },
  {
    id: "creative",
    get label() {
      return t("models.compatPresetCreative");
    },
    context_length: 8192,
    temperature: 1.1,
    get summary() {
      return t("models.compatPresetCreativeSum");
    },
    get vramHint() {
      return t("models.compatPresetCreativeVram");
    },
  },
];

export function formatColabOneGb(gb: number): string {
  if (!Number.isFinite(gb) || gb <= 0) return "0 GB";
  if (gb >= 10) return `${Math.round(gb * 10) / 10} GB`.replace(/\.0 GB$/, " GB");
  return `${gb.toFixed(1)} GB`;
}

export function formatColabOneFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return t("common.unknown");
  if (bytes < 1024 ** 3) {
    return `${(bytes / 1024 ** 2).toFixed(0)} MiB`;
  }
  return `${(bytes / 1024 ** 3).toFixed(2)} GiB`;
}

function liveGpuDisplayName(rawName: string, vramGb: number): string {
  const upper = (rawName || "").toUpperCase();
  if (/PRO\s*6000|RTX\s*6000|BLACKWELL|\bG4\b/.test(upper) || vramGb >= 90) {
    return `G4 ${vramGb}G`;
  }
  if (/H100/.test(upper)) return `H100 ${vramGb}G`;
  if (/A100/.test(upper)) return `A100 ${vramGb}G`;
  if (/\bL4\b/.test(upper)) return `L4 ${vramGb}G`;
  if (/\bT4\b/.test(upper)) return `T4 ${vramGb}G`;
  const trimmed = rawName.replace(/^NVIDIA\s+/i, "").trim();
  return trimmed ? `${trimmed} ${vramGb}G` : `${vramGb}G GPU`;
}

function inferDefaultHostRamGb(vramGb: number): number {
  if (vramGb >= 90) return 170;
  if (vramGb >= 70) return 165;
  if (vramGb >= 36) return 83;
  if (vramGb >= 20) return 53;
  return 13;
}

function inferDefaultDiskFreeGb(vramGb: number): number {
  return vramGb >= 20 ? 195 : 75;
}

function inferDefaultDiskTotalGb(vramGb: number): number {
  return vramGb >= 20 ? 235 : 112;
}

export function estimateColabOneDiskGb(weightBytes: number): number {
  if (!Number.isFinite(weightBytes) || weightBytes <= 0) return 0;
  return weightBytes / 1024 ** 3;
}

export function mapSettingsGpuToTargetId(
  settingsGpu?: string | null,
): ColabOneGpuTargetId {
  switch ((settingsGpu || "").toUpperCase()) {
    case "G4":
      return "G4";
    case "H100":
      return "H100";
    case "A100":
      return "A100_80";
    case "L4":
      return "L4";
    case "T4":
      return "T4";
    default:
      return COLABONE_DEFAULT_GPU;
  }
}

export function hasLiveRuntimeGpu(runtime?: RuntimeStatus | null): boolean {
  return Boolean(
    runtime &&
      runtime.state !== "stopped" &&
      runtime.gpu &&
      runtime.gpu.total_mib > 0,
  );
}

export function resolveColabOneDefaultGpuTarget(
  runtime?: RuntimeStatus | null,
  settingsGpu?: string | null,
): ColabOneGpuTargetId {
  if (hasLiveRuntimeGpu(runtime)) return "CURRENT";
  return mapSettingsGpuToTargetId(settingsGpu);
}

export function buildColabOneGpuTargetOptions(
  runtime?: RuntimeStatus | null,
): ColabOneGpuTarget[] {
  const list: ColabOneGpuTarget[] = [];
  if (hasLiveRuntimeGpu(runtime) && runtime?.gpu) {
    const vramMib = runtime.gpu.total_mib;
    const vramGb = Math.max(1, Math.round(vramMib / 1024));
    const localizedGpuName = tb(runtime.gpu.name || "");
    const shortName = liveGpuDisplayName(localizedGpuName, vramGb);
    const liveRamBytes = runtime.resources?.memory?.total_bytes || 0;
    const hostRamGb =
      liveRamBytes > 0
        ? Math.max(4, Math.round(liveRamBytes / 1024 ** 3))
        : inferDefaultHostRamGb(vramGb);
    const liveDiskFreeBytes = resolveColabOneDiskFreeBytes(runtime);
    const liveDiskTotalBytes = runtime.resources?.disk?.total_bytes || 0;
    const isLiveDisk = liveDiskFreeBytes !== null;
    const diskFreeGb =
      liveDiskFreeBytes !== null
        ? Math.round((liveDiskFreeBytes / 1024 ** 3) * 10) / 10
        : inferDefaultDiskFreeGb(vramGb);
    const diskTotalGb = Math.max(
      Math.ceil(diskFreeGb),
      liveDiskTotalBytes > 0
        ? Math.max(1, Math.round(liveDiskTotalBytes / 1024 ** 3))
        : inferDefaultDiskTotalGb(vramGb),
    );
    const baseSub = localizedGpuName || t("models.compatLiveGpuSub");
    const diskSub = isLiveDisk
      ? t("models.compatGpuDiskLive", {
          gb: String(diskFreeGb).replace(/\.0$/, ""),
        })
      : t("models.compatGpuDiskApprox", { gb: diskFreeGb });
    list.push({
      id: "CURRENT",
      shortLabel: shortName,
      label: t("models.compatLiveGpuLabel", { name: shortName }),
      subtitle: `${baseSub} · ${diskSub}`,
      vramGb,
      vramMib,
      hostRamGb,
      diskFreeGb,
      diskTotalGb,
      isLiveDisk,
      isLive: true,
    });
  }
  return [...list, ...COLABONE_STATIC_GPU_TARGETS];
}

export function resolveColabOneGpuTarget(
  targetId: ColabOneGpuTargetId,
  runtime?: RuntimeStatus | null,
  settingsGpu?: string | null,
): ColabOneGpuTarget {
  const options = buildColabOneGpuTargetOptions(runtime);
  const exact = options.find((opt) => opt.id === targetId);
  if (exact) return exact;
  const fallbackId = mapSettingsGpuToTargetId(settingsGpu);
  return (
    options.find((opt) => opt.id === fallbackId) ||
    COLABONE_STATIC_GPU_TARGETS[0]!
  );
}

export function estimateColabOneKvCacheMib(
  weightBytes: number,
  contextLength = 8192,
): number {
  if (!Number.isFinite(weightBytes) || weightBytes <= 0) return 0;
  if (!Number.isFinite(contextLength) || contextLength <= 0) return 0;
  const weightMib = weightBytes / (1024 * 1024);
  if (weightMib < 64) return 0;
  const ctxFactor = Math.max(0.5, contextLength / 8192);
  const baseKv8kMib =
    weightMib < 4096
      ? 512
      : weightMib < 20480
        ? 1024
        : weightMib < 50000
          ? 1536
          : 2560;
  return Math.round(baseKv8kMib * ctxFactor);
}

export function estimateColabOneVramMib(
  weightBytes: number,
  contextLength = 8192,
): number {
  if (!Number.isFinite(weightBytes) || weightBytes <= 0) return 0;
  const weightMib = weightBytes / (1024 * 1024);
  if (weightMib < 64) {
    return Math.max(1, Math.round(weightMib * 1.15));
  }
  const runtimeOverheadMib =
    weightMib < 512 ? weightMib * 0.15 + 128 : weightMib * 0.06 + 512;
  const kvMib = estimateColabOneKvCacheMib(weightBytes, contextLength);
  return Math.round(weightMib + runtimeOverheadMib + kvMib);
}

export function estimateColabOneModelVramMib(
  model: Pick<ModelSpec, "size" | "min_vram_mib" | "context_length">,
  contextLength?: number,
): number {
  const effectiveCtx = contextLength || model.context_length || 8192;
  const fromSize =
    model.size > 0 ? estimateColabOneVramMib(model.size, effectiveCtx) : 0;
  if (fromSize > 0) {
    return fromSize;
  }
  if (model.min_vram_mib > 0) {
    const extraCtxMib =
      effectiveCtx > (model.context_length || 8192)
        ? Math.max(
            0,
            estimateColabOneKvCacheMib(
              model.min_vram_mib * 1024 ** 2,
              effectiveCtx,
            ) -
              estimateColabOneKvCacheMib(
                model.min_vram_mib * 1024 ** 2,
                model.context_length || 8192,
              ),
          )
        : 0;
    return Math.round(model.min_vram_mib + extraCtxMib);
  }
  return 0;
}

export function recommendColabOneMinGpu(
  estimatedVramMib: number,
  comfyOnly = false,
): ColabOneMinGpuHint {
  const tiers: {
    id: Exclude<ColabOneGpuTargetId, "CURRENT" | "ALL">;
    label: string;
    maxMib: number;
  }[] = [
    { id: "T4", label: "T4 16G", maxMib: 16 * 1024 * 0.98 },
    { id: "L4", label: "L4 24G", maxMib: 24 * 1024 * 0.98 },
    { id: "A100_40", label: "A100 40G", maxMib: 40 * 1024 * 0.98 },
    { id: "A100_80", label: "H100 / A100 80G", maxMib: 80 * 1024 * 0.98 },
    { id: "G4", label: "G4 96G", maxMib: 96 * 1024 * 0.98 },
  ];
  for (const tier of tiers) {
    if (estimatedVramMib <= tier.maxMib) {
      return { id: tier.id, label: tier.label };
    }
  }
  return {
    id: "OVER_96G",
    label: comfyOnly
      ? t("models.compatOver96")
      : t("models.compatOver96Hybrid"),
  };
}

export function evaluateColabOneVramFit(
  estimatedVramMib: number,
  targetGpu: ColabOneGpuTarget,
  comfyOnly = false,
): ColabOneVramFit {
  const estimatedVramGb = estimatedVramMib / 1024;
  const minGpu = recommendColabOneMinGpu(estimatedVramMib, comfyOnly);

  if (targetGpu.id === "ALL" || targetGpu.vramMib <= 0) {
    return {
      status: "unrestricted",
      ratio: 0,
      estimatedVramMib,
      estimatedVramGb,
      overflowGb: 0,
      label:
        minGpu.id === "OVER_96G"
          ? comfyOnly
            ? t("models.compatOver96Short")
            : t("models.compatOver96HybridShort")
          : t("models.compatSuggestGpu", { gpu: minGpu.label }),
      detail: t("models.compatEstVramDetail", {
        gb: formatColabOneGb(estimatedVramGb),
      }),
      selectable: true,
      requiresHybrid: false,
      minGpu,
    };
  }

  const ratio = estimatedVramMib / targetGpu.vramMib;
  if (ratio <= 0.85) {
    return {
      status: "comfortable",
      ratio,
      estimatedVramMib,
      estimatedVramGb,
      overflowGb: 0,
      label: t("models.compatFitsLabel", { gpu: targetGpu.shortLabel }),
      detail: t("models.compatFitsDetail", {
        gb: formatColabOneGb(estimatedVramGb),
        vram: targetGpu.vramGb,
      }),
      selectable: true,
      requiresHybrid: false,
      minGpu,
    };
  }
  if (ratio <= 0.98) {
    return {
      status: "tight",
      ratio,
      estimatedVramMib,
      estimatedVramGb,
      overflowGb: 0,
      label: t("models.compatTightLabel"),
      detail: t("models.compatTightDetail", {
        gb: formatColabOneGb(estimatedVramGb),
        vram: targetGpu.vramGb,
      }),
      selectable: true,
      requiresHybrid: false,
      minGpu,
    };
  }

  const rawOverflowGb = estimatedVramGb - targetGpu.vramGb;
  const overflowGb = rawOverflowGb > 0.1 ? Math.max(0.5, rawOverflowGb) : 0;

  if (comfyOnly) {
    return {
      status: "heavy_hybrid",
      ratio,
      estimatedVramMib,
      estimatedVramGb,
      overflowGb,
      label: t("models.compatOverflowLabel"),
      detail:
        minGpu.id === "OVER_96G"
          ? t("models.compatOverflowDetail96", {
              gb: formatColabOneGb(estimatedVramGb),
            })
          : t("models.compatOverflowDetail", {
              gb: formatColabOneGb(estimatedVramGb),
              gpu: targetGpu.shortLabel,
              minGpu: minGpu.label,
            }),
      selectable: false,
      requiresHybrid: false,
      minGpu,
    };
  }

  const hybridCapGb = targetGpu.vramGb + Math.max(8, targetGpu.hostRamGb * 0.85);
  if (estimatedVramGb <= hybridCapGb) {
    return {
      status: "hybrid",
      ratio,
      estimatedVramMib,
      estimatedVramGb,
      overflowGb,
      label: t("models.compatHybridLabel"),
      detail:
        overflowGb > 0
          ? t("models.compatHybridDetail", {
              gpu: targetGpu.shortLabel,
              overflow: formatColabOneGb(overflowGb),
            })
          : t("models.compatHybridNearLimitDetail", {
              gb: formatColabOneGb(estimatedVramGb),
              gpu: targetGpu.shortLabel,
            }),
      selectable: true,
      requiresHybrid: true,
      minGpu,
    };
  }

  return {
    status: "heavy_hybrid",
    ratio,
    estimatedVramMib,
    estimatedVramGb,
    overflowGb,
    label: t("models.compatHeavyHybridLabel"),
    detail: t("models.compatHeavyHybridDetail", {
      gb: formatColabOneGb(estimatedVramGb),
      gpu: targetGpu.shortLabel,
      minGpu: minGpu.label,
    }),
    selectable: true,
    requiresHybrid: true,
    minGpu,
  };
}

export function describeColabOneQuantization(
  quantization: string,
  filename = "",
): ColabOneQuantInfo {
  const raw = (quantization || "").trim().toUpperCase();
  const code = raw && raw !== "GGUF" ? raw : inferQuantFromFilename(filename);

  if (/^(?:BF16|F16|F32)$/.test(code)) {
    return {
      code,
      title: t("models.compatQuantFpTitle"),
      desc: t("models.compatQuantFpDesc"),
      bucket: "lossless",
      qualityTier: 60,
      defaultScore: 74,
    };
  }
  if (/^(?:UD-)?Q8(?:_|$)|^FP8/.test(code)) {
    return {
      code,
      title: t("models.compatQuantQ8Title"),
      desc: t("models.compatQuantQ8Desc"),
      bucket: "high",
      qualityTier: 52,
      defaultScore: 90,
    };
  }
  if (/^(?:UD-)?Q6(?:_|$)/.test(code)) {
    return {
      code,
      title: t("models.compatQuantQ6Title"),
      desc: t("models.compatQuantQ6Desc"),
      bucket: "high",
      qualityTier: 48,
      defaultScore: 94,
    };
  }
  if (/^(?:UD-)?Q5(?:_|$)/.test(code)) {
    const isMediumOrLarge = /K_M|K_L|K_XL|_1/.test(code);
    return {
      code,
      title: t("models.compatQuantQ5Title"),
      desc: t("models.compatQuantQ5Desc"),
      bucket: "balanced",
      qualityTier: isMediumOrLarge ? 44 : 42,
      defaultScore: isMediumOrLarge ? 95 : 91,
    };
  }
  if (/^(?:UD-)?(?:Q4|IQ4)(?:_|$)|^MXFP4/.test(code)) {
    const isSweetSpot = /Q4_K_M|UD-Q4_K_XL|Q4_K_XL|Q4_K_L|MXFP4/.test(code);
    const isCompact4 = /Q4_K_S|IQ4_XS|Q4_0/.test(code);
    return {
      code,
      title: isCompact4
        ? t("models.compatQuantQ4LightTitle")
        : t("models.compatQuantQ4MainTitle"),
      desc: isCompact4
        ? t("models.compatQuantQ4LightDesc")
        : t("models.compatQuantQ4MainDesc"),
      bucket: isCompact4 ? "compact" : "balanced",
      qualityTier: isSweetSpot ? 38 : isCompact4 ? 34 : 36,
      defaultScore: isSweetSpot ? 100 : isCompact4 ? 86 : 92,
    };
  }
  if (/^(?:UD-)?(?:Q3|IQ3)(?:_|$)/.test(code)) {
    return {
      code,
      title: t("models.compatQuantQ3Title"),
      desc: t("models.compatQuantQ3Desc"),
      bucket: "compact",
      qualityTier: 25,
      defaultScore: 62,
    };
  }
  if (/^(?:UD-)?(?:Q2|IQ2|IQ1|TQ)(?:_|$)/.test(code)) {
    return {
      code,
      title: t("models.compatQuantQ2Title"),
      desc: t("models.compatQuantQ2Desc"),
      bucket: "extreme",
      qualityTier: 12,
      defaultScore: 32,
    };
  }
  return {
    code: code || "GGUF",
    title: t("models.compatQuantStdTitle"),
    desc: t("models.compatQuantStdDesc"),
    bucket: "balanced",
    qualityTier: 35,
    defaultScore: 70,
  };
}

function inferQuantFromFilename(filename: string): string {
  const clean = (filename || "")
    .replace(/(?:-\d{5}-of-\d{5})?\.gguf$/i, "")
    .trim();
  const patterns = [
    /(?<![A-Za-z0-9])(UD-(?:IQ|Q|TQ)\d[A-Za-z0-9_]*)(?![A-Za-z0-9])/i,
    /(?<![A-Za-z0-9])((?:IQ|Q|TQ)\d(?:_[01K](?:_[A-Za-z0-9]+)?|_[A-Za-z]{1,4}))(?![A-Za-z0-9])/i,
    /(?<![A-Za-z0-9])(MXFP4(?:_MOE)?|FP8(?:_E4M3(?:FN)?|_E5M2)?|BF16|F16|F32)(?![A-Za-z0-9])/i,
    /(?<![A-Za-z0-9])(Q[2-8])(?![A-Za-z0-9])/i,
  ];
  for (const re of patterns) {
    const m = clean.match(re);
    if (m && m[1]) return m[1].toUpperCase();
  }
  return "GGUF";
}

const COLABONE_DISK_SAFETY_MARGIN_GB = 0.5;

export function evaluateColabOneTargetDiskFit(
  weightBytes: number,
  targetGpu: ColabOneGpuTarget,
  state?: ColabOneFileDiskState,
): ColabOneTargetDiskFit {
  const requiredGb = estimateColabOneDiskGb(weightBytes);
  const availableGb = Math.max(0, targetGpu.diskFreeGb || 0);
  const totalGb = Math.max(availableGb, targetGpu.diskTotalGb || 0);
  const isLive = !!targetGpu.isLiveDisk;
  // Cached/downloaded state only applies to the currently connected live VM,
  // never to hypothetical static GPU tier previews (e.g., checking T4 while on G4).
  const liveState = targetGpu.isLive ? state : undefined;

  if (liveState?.downloaded) {
    return {
      status: "cached",
      requiredGb,
      remainingGb: 0,
      availableGb,
      totalGb,
      isLive,
      ratio: 0,
      insufficient: false,
      exceedsVm: false,
      label: "",
      detail: "",
    };
  }

  if (
    targetGpu.id === "ALL" ||
    requiredGb <= 0 ||
    (availableGb <= 0 && !isLive)
  ) {
    return {
      status: "unrestricted",
      requiredGb,
      remainingGb: requiredGb,
      availableGb,
      totalGb,
      isLive,
      ratio: 0,
      insufficient: false,
      exceedsVm: false,
      label: "",
      detail: "",
    };
  }

  const cachedBytes = Math.max(0, liveState?.cachedBytes || 0);
  const remainingGb = Math.max(0, (weightBytes - cachedBytes) / 1024 ** 3);
  if (remainingGb <= 0) {
    return {
      status: "cached",
      requiredGb,
      remainingGb: 0,
      availableGb,
      totalGb,
      isLive,
      ratio: 0,
      insufficient: false,
      exceedsVm: false,
      label: "",
      detail: "",
    };
  }

  const neededWithMargin = remainingGb + COLABONE_DISK_SAFETY_MARGIN_GB;
  const ratio = availableGb > 0 ? neededWithMargin / availableGb : Infinity;

  if (neededWithMargin > availableGb) {
    // On a live VM, distinguish "exceeds current free disk (can clear other cached models)"
    // from "exceeds the VM's entire disk capacity".
    const exceedsVm = isLive
      ? requiredGb + COLABONE_DISK_SAFETY_MARGIN_GB > Math.max(availableGb, totalGb - 1)
      : true;
    if (exceedsVm) {
      return {
        status: "exceeds_vm",
        requiredGb,
        remainingGb,
        availableGb,
        totalGb,
        isLive,
        ratio,
        insufficient: true,
        exceedsVm: true,
        label: t("models.compatDiskExceedsLabel"),
        detail: t("models.compatDiskExceedsVmDetail", {
          need: formatColabOneGb(requiredGb),
          gpu: targetGpu.shortLabel,
          avail: formatColabOneGb(availableGb),
          total: formatColabOneGb(totalGb),
        }),
      };
    }
    return {
      status: "insufficient_live",
      requiredGb,
      remainingGb,
      availableGb,
      totalGb,
      isLive,
      ratio,
      insufficient: true,
      exceedsVm: false,
      label: t("models.compatDiskLowLabel"),
      detail: t("models.compatDiskLowLiveDetail", {
        need: formatColabOneGb(remainingGb),
        avail: formatColabOneGb(availableGb),
        total: formatColabOneGb(totalGb),
      }),
    };
  }

  if (ratio > 0.85) {
    return {
      status: "tight",
      requiredGb,
      remainingGb,
      availableGb,
      totalGb,
      isLive,
      ratio,
      insufficient: false,
      exceedsVm: false,
      label: t("models.compatDiskTightLabel"),
      detail: t("models.compatDiskTightDetail", {
        need: formatColabOneGb(remainingGb),
        avail: formatColabOneGb(availableGb),
      }),
    };
  }

  return {
    status: "ok",
    requiredGb,
    remainingGb,
    availableGb,
    totalGb,
    isLive,
    ratio,
    insufficient: false,
    exceedsVm: false,
    label: "",
    detail: "",
  };
}

function diskTierRank(disk: ColabOneTargetDiskFit): number {
  if (!disk.insufficient) return 0;
  return disk.exceedsVm ? 2 : 1;
}

function fitTierRank(status: ColabOneVramFitStatus): number {
  switch (status) {
    case "comfortable":
    case "unrestricted":
      return 0;
    case "tight":
      return 1;
    case "hybrid":
      return 2;
    case "heavy_hybrid":
      return 3;
  }
}

export function planColabOneVersionOptions(
  files: ColabOneHubFile[],
  targetGpu: ColabOneGpuTarget,
  contextLength = 8192,
  selectedOrPreferred = "",
  savedFilenames?: ReadonlySet<string>,
  comfyOnly = false,
  fileDiskStates?: ReadonlyMap<string, ColabOneFileDiskState>,
): ColabOneVersionPlan {
  const effectiveContext = comfyOnly ? 0 : contextLength;
  const evaluated: ColabOneVersionOption[] = files.map((file) => {
    const quant = describeColabOneQuantization(
      file.quantization,
      file.filename,
    );
    const estMib = estimateColabOneVramMib(file.size, effectiveContext);
    const fit = evaluateColabOneVramFit(estMib, targetGpu, comfyOnly);
    const disk = evaluateColabOneTargetDiskFit(
      file.size,
      targetGpu,
      fileDiskStates?.get(file.filename),
    );
    // Only broken/incomplete GGUF files are hard-disabled; oversized valid files remain selectable
    const disabled = !file.supported;
    const disabledReason = !file.supported
      ? tb(file.reason) || t("models.compatMissingShards")
      : "";
    return {
      file,
      quant,
      fit,
      disk,
      recommended: false,
      disabled,
      disabledReason,
    };
  });

  // Pick single best recommended option among supported files:
  // prefer files that fit on disk first, then full-VRAM comfortable -> tight -> hybrid -> heavy_hybrid
  const eligible = evaluated.filter((item) => !item.disabled);
  let recommended: ColabOneVersionOption | undefined;
  if (eligible.length) {
    const sortedForRec = [...eligible].sort((a, b) => {
      const diskDiff = diskTierRank(a.disk) - diskTierRank(b.disk);
      if (diskDiff !== 0) return diskDiff;
      const tierDiff = fitTierRank(a.fit.status) - fitTierRank(b.fit.status);
      if (tierDiff !== 0) return tierDiff;
      if (b.quant.defaultScore !== a.quant.defaultScore) {
        return b.quant.defaultScore - a.quant.defaultScore;
      }
      if (b.quant.qualityTier !== a.quant.qualityTier) {
        return b.quant.qualityTier - a.quant.qualityTier;
      }
      return a.file.size - b.file.size;
    });
    recommended = sortedForRec[0];
    if (recommended && !recommended.disk.exceedsVm) {
      recommended.recommended = true;
    }
  }

  // Order all options cleanly:
  // 1. Recommended
  // 2. Fits on disk -> live low disk -> exceeds VM disk
  // 3. Full-VRAM comfortable -> tight -> Hybrid GPU+RAM -> heavy_hybrid
  // 4. Unsupported metadata
  const ordered = [...evaluated].sort((a, b) => {
    if (a.recommended !== b.recommended) return a.recommended ? -1 : 1;
    if (a.file.supported !== b.file.supported) return a.file.supported ? -1 : 1;
    const dDiff = diskTierRank(a.disk) - diskTierRank(b.disk);
    if (dDiff !== 0) return dDiff;
    const gDiff = fitTierRank(a.fit.status) - fitTierRank(b.fit.status);
    if (gDiff !== 0) return gDiff;
    if (fitTierRank(a.fit.status) >= 2) {
      return a.file.size - b.file.size;
    }
    if (b.quant.qualityTier !== a.quant.qualityTier) {
      return b.quant.qualityTier - a.quant.qualityTier;
    }
    return a.file.size - b.file.size;
  });

  // Build beginner-friendly curated 3-4 card view when there are more than 4 versions
  let curated: ColabOneVersionOption[];
  if (ordered.length <= 4) {
    curated = ordered;
  } else {
    const picked = new Map<string, ColabOneVersionOption>();
    const add = (opt?: ColabOneVersionOption) => {
      if (opt && !picked.has(opt.file.filename)) {
        picked.set(opt.file.filename, opt);
      }
    };
    add(recommended);
    if (selectedOrPreferred) {
      add(ordered.find((o) => o.file.filename === selectedOrPreferred));
    }
    if (savedFilenames && savedFilenames.size > 0) {
      for (const o of ordered) {
        if (savedFilenames.has(o.file.filename) && picked.size < 4) {
          add(o);
        }
      }
    }
    const diskOkOrdered = ordered.filter(
      (o) => !o.disabled && !o.disk.exceedsVm,
    );
    const activeOrdered = diskOkOrdered.length ? diskOkOrdered : ordered;
    const fullVramPool = activeOrdered.filter(
      (o) => !o.disabled && !o.fit.requiresHybrid,
    );
    const hybridPool = activeOrdered.filter(
      (o) => !o.disabled && o.fit.requiresHybrid,
    );
    const preferredPool = fullVramPool.length ? fullVramPool : hybridPool;

    const highPick = preferredPool.find(
      (o) =>
        !picked.has(o.file.filename) &&
        (o.quant.bucket === "high" || o.quant.bucket === "lossless"),
    );
    add(highPick);
    const balancedPick = preferredPool.find(
      (o) => !picked.has(o.file.filename) && o.quant.bucket === "balanced",
    );
    if (picked.size < 4) add(balancedPick);
    const compactPick = preferredPool.find(
      (o) => !picked.has(o.file.filename) && o.quant.bucket === "compact",
    );
    if (picked.size < 4) add(compactPick);
    for (const o of preferredPool) {
      if (picked.size >= 3) break;
      add(o);
    }
    // Include up to 1 representative hybrid option so users also see larger GPU+RAM options immediately
    if (fullVramPool.length > 0 && hybridPool.length > 0 && picked.size < 4) {
      add(hybridPool[0]);
    }
    for (const o of ordered) {
      if (picked.size >= 4) break;
      add(o);
    }
    curated = ordered.filter((o) => picked.has(o.file.filename));
  }

  const fullVramCount = ordered.filter(
    (o) =>
      !o.disabled &&
      !o.disk.exceedsVm &&
      !o.fit.requiresHybrid &&
      (!comfyOnly ||
        o.fit.status === "comfortable" ||
        o.fit.status === "tight" ||
        o.fit.status === "unrestricted"),
  ).length;
  const hybridCount = ordered.filter(
    (o) => !o.disabled && !o.disk.exceedsVm && o.fit.requiresHybrid,
  ).length;
  const diskFittingCount = ordered.filter(
    (o) => !o.disabled && !o.disk.exceedsVm,
  ).length;
  const diskInsufficientCount = ordered.filter(
    (o) => !o.disabled && o.disk.insufficient,
  ).length;
  const diskExceedsVmCount = ordered.filter(
    (o) => !o.disabled && o.disk.exceedsVm,
  ).length;
  const unsupportedCount = ordered.filter((o) => !o.file.supported).length;
  const curatedNames = new Set(curated.map((o) => o.file.filename));
  const hiddenHybridCount = ordered.filter(
    (o) =>
      !curatedNames.has(o.file.filename) &&
      !o.disabled &&
      !o.disk.exceedsVm &&
      o.fit.requiresHybrid,
  ).length;

  // Suggest larger GPU for pure-VRAM running when all versions require hybrid offloading
  let suggestedGpuId: Exclude<ColabOneGpuTargetId, "CURRENT" | "ALL"> | null =
    null;
  let suggestedGpuLabel = "";
  const smallestSupported = evaluated
    .filter((o) => o.file.supported && o.file.size > 0)
    .sort((a, b) => a.file.size - b.file.size)[0];
  if (fullVramCount === 0 && hybridCount > 0 && smallestSupported) {
    if (smallestSupported.fit.minGpu.id !== "OVER_96G") {
      const candId = smallestSupported.fit.minGpu.id;
      const candTarget = COLABONE_STATIC_GPU_TARGETS.find(
        (g) => g.id === candId,
      );
      if (candTarget && candTarget.vramGb >= targetGpu.vramGb + 4) {
        suggestedGpuId = candId;
        suggestedGpuLabel = smallestSupported.fit.minGpu.label;
      }
    }
  }

  // Suggest a GPU tier with larger local disk when all versions exceed current GPU disk capacity
  let suggestedDiskGpuId: Exclude<ColabOneGpuTargetId, "CURRENT" | "ALL"> | null =
    null;
  let suggestedDiskGpuLabel = "";
  let suggestedDiskAvailGb = 0;
  if (diskFittingCount === 0 && diskExceedsVmCount > 0 && smallestSupported) {
    const neededGb =
      estimateColabOneDiskGb(smallestSupported.file.size) +
      COLABONE_DISK_SAFETY_MARGIN_GB;
    const diskCandidates = [...COLABONE_STATIC_GPU_TARGETS]
      .filter(
        (g): g is ColabOneGpuTarget & { id: Exclude<ColabOneGpuTargetId, "CURRENT" | "ALL"> } =>
          g.id !== "ALL" &&
          g.diskFreeGb > targetGpu.diskFreeGb + 10 &&
          g.diskFreeGb >= neededGb,
      )
      .sort((a, b) => a.vramGb - b.vramGb);
    if (diskCandidates.length > 0) {
      // Pick the smallest GPU tier whose VRAM also fits if possible, else the first larger-disk tier (e.g. L4)
      const best =
        diskCandidates.find(
          (g) => smallestSupported.fit.estimatedVramMib <= g.vramMib * 0.98,
        ) || diskCandidates[0]!;
      suggestedDiskGpuId = best.id;
      suggestedDiskGpuLabel = best.shortLabel;
      suggestedDiskAvailGb = best.diskFreeGb;
    }
  }

  return {
    all: ordered,
    curated,
    recommendedFilename: recommended?.file.filename || "",
    fullVramCount,
    hybridCount,
    diskFittingCount,
    diskInsufficientCount,
    diskExceedsVmCount,
    unsupportedCount,
    totalCount: ordered.length,
    hasHidden: ordered.length > curated.length,
    hiddenHybridCount,
    suggestedGpuId,
    suggestedGpuLabel,
    suggestedDiskGpuId,
    suggestedDiskGpuLabel,
    suggestedDiskAvailGb,
  };
}

export function summarizeColabOneRepoFit(
  files: ColabOneHubFile[],
  targetGpu: ColabOneGpuTarget,
  contextLength = 8192,
  comfyOnly = false,
  fileDiskStates?: ReadonlyMap<string, ColabOneFileDiskState>,
): ColabOneRepoFitSummary {
  const supported = files.filter((f) => f.supported && f.size > 0);
  if (!supported.length) {
    return {
      status: "unknown",
      fullVramCount: 0,
      hybridCount: 0,
      diskFittingCount: 0,
      diskExceedsVm: false,
      totalSupported: 0,
      minEstimatedGb: 0,
      maxEstimatedGb: 0,
      recommendedQuant: "",
      badgeLabel: "",
      minGpuHint: "",
      runnable: true,
    };
  }
  const plan = planColabOneVersionOptions(
    supported,
    targetGpu,
    contextLength,
    "",
    undefined,
    comfyOnly,
    fileDiskStates,
  );
  const estGbs = plan.all.map((o) => o.fit.estimatedVramGb);
  const minEstimatedGb = Math.min(...estGbs);
  const maxEstimatedGb = Math.max(...estGbs);
  const rec = plan.all.find((o) => o.recommended);
  const smallest = [...plan.all].sort((a, b) => a.file.size - b.file.size)[0]!;

  if (targetGpu.id === "ALL" || targetGpu.vramMib <= 0) {
    return {
      status: "unrestricted",
      fullVramCount: plan.fullVramCount,
      hybridCount: plan.hybridCount,
      diskFittingCount: plan.diskFittingCount,
      diskExceedsVm: false,
      totalSupported: supported.length,
      minEstimatedGb,
      maxEstimatedGb,
      recommendedQuant: rec?.quant.code || "",
      badgeLabel:
        smallest.fit.minGpu.id === "OVER_96G"
          ? comfyOnly
            ? t("models.compatOver96Short")
            : t("models.compatOver96HybridShort")
          : t("models.compatSuggestGpu", { gpu: smallest.fit.minGpu.label }),
      minGpuHint: smallest.fit.minGpu.label,
      runnable: true,
    };
  }

  if (plan.diskFittingCount === 0 && plan.diskExceedsVmCount > 0) {
    return {
      status: "disk_overflow",
      fullVramCount: 0,
      hybridCount: 0,
      diskFittingCount: 0,
      diskExceedsVm: true,
      totalSupported: supported.length,
      minEstimatedGb,
      maxEstimatedGb,
      recommendedQuant: rec?.quant.code || "",
      badgeLabel: t("models.compatDiskOverflowRepoLabel", {
        gpu: targetGpu.shortLabel,
      }),
      minGpuHint: smallest.fit.minGpu.label,
      runnable: false,
    };
  }

  if (plan.fullVramCount > 0) {
    const hasComfortable = plan.all.some(
      (o) => !o.disabled && !o.disk.exceedsVm && o.fit.status === "comfortable",
    );
    return {
      status: hasComfortable ? "compatible" : "tight",
      fullVramCount: plan.fullVramCount,
      hybridCount: plan.hybridCount,
      diskFittingCount: plan.diskFittingCount,
      diskExceedsVm: false,
      totalSupported: supported.length,
      minEstimatedGb,
      maxEstimatedGb,
      recommendedQuant: rec?.quant.code || "",
      badgeLabel: hasComfortable
        ? t("models.compatFitsLabel", { gpu: targetGpu.shortLabel })
        : t("models.compatTightRepoLabel", { gpu: targetGpu.shortLabel }),
      minGpuHint: smallest.fit.minGpu.label,
      runnable: true,
    };
  }

  if (comfyOnly) {
    return {
      status: "heavy_hybrid",
      fullVramCount: 0,
      hybridCount: 0,
      diskFittingCount: plan.diskFittingCount,
      diskExceedsVm: false,
      totalSupported: supported.length,
      minEstimatedGb,
      maxEstimatedGb,
      recommendedQuant: rec?.quant.code || "",
      badgeLabel:
        smallest.fit.minGpu.id === "OVER_96G"
          ? t("models.compatOver96Short")
          : t("models.compatOverflowRepoLabel", {
              gpu: targetGpu.shortLabel,
              minGpu: smallest.fit.minGpu.label,
            }),
      minGpuHint: smallest.fit.minGpu.label,
      runnable: false,
    };
  }

  const hasStandardHybrid = plan.all.some(
    (o) => !o.disabled && !o.disk.exceedsVm && o.fit.status === "hybrid",
  );
  if (hasStandardHybrid) {
    return {
      status: "hybrid",
      fullVramCount: 0,
      hybridCount: plan.hybridCount,
      diskFittingCount: plan.diskFittingCount,
      diskExceedsVm: false,
      totalSupported: supported.length,
      minEstimatedGb,
      maxEstimatedGb,
      recommendedQuant: rec?.quant.code || "",
      badgeLabel: t("models.compatHybridRepoLabel", {
        gpu: targetGpu.shortLabel,
      }),
      minGpuHint: smallest.fit.minGpu.label,
      runnable: true,
    };
  }

  return {
    status: "heavy_hybrid",
    fullVramCount: 0,
    hybridCount: plan.hybridCount,
    diskFittingCount: plan.diskFittingCount,
    diskExceedsVm: false,
    totalSupported: supported.length,
    minEstimatedGb,
    maxEstimatedGb,
    recommendedQuant: rec?.quant.code || "",
    badgeLabel: t("models.compatHeavyHybridRepoLabel", {
      minGpu: smallest.fit.minGpu.label,
    }),
    minGpuHint: smallest.fit.minGpu.label,
    runnable: false,
  };
}

export function matchColabOneParamPreset(
  contextLength: number,
  temperature: number,
): ColabOneParamPreset | null {
  const roundedTemp = Math.round(temperature * 10) / 10;
  return (
    COLABONE_PARAM_PRESETS.find(
      (p) =>
        p.context_length === contextLength &&
        Math.round(p.temperature * 10) / 10 === roundedTemp,
    ) || null
  );
}

export function describeColabOneRuntimeParams(
  contextLength: number,
  temperature: number,
): {
  preset: ColabOneParamPreset | null;
  presetTitle: string;
  contextK: number;
  contextEffect: string;
  temperatureEffect: string;
  combinedEffect: string;
} {
  const preset = matchColabOneParamPreset(contextLength, temperature);
  const contextK = Math.max(1, Math.round(contextLength / 1024));

  let contextEffect: string;
  if (contextLength <= 4096) {
    contextEffect = t("models.compatEffectCtx4k");
  } else if (contextLength <= 8192) {
    contextEffect = t("models.compatEffectCtx8k");
  } else if (contextLength <= 16384) {
    contextEffect = t("models.compatEffectCtx16k");
  } else if (contextLength <= 32768) {
    contextEffect = t("models.compatEffectCtx32k");
  } else if (contextLength <= 65536) {
    contextEffect = t("models.compatEffectCtx64k");
  } else {
    contextEffect = t("models.compatEffectCtx128k");
  }

  let temperatureEffect: string;
  if (temperature <= 0.3) {
    temperatureEffect = t("models.compatEffectTempLow");
  } else if (temperature <= 0.8) {
    temperatureEffect = t("models.compatEffectTempMid");
  } else if (temperature <= 1.3) {
    temperatureEffect = t("models.compatEffectTempHigh");
  } else {
    temperatureEffect = t("models.compatEffectTempMax");
  }

  const presetTitle = preset
    ? preset.badge
      ? t("models.compatPresetWithBadge", {
          label: preset.label,
          badge: preset.badge,
        })
      : preset.label
    : t("models.compatCustomPreset");

  return {
    preset,
    presetTitle,
    contextK,
    contextEffect,
    temperatureEffect,
    combinedEffect: t("models.compatCombinedEffect", {
      ctx: contextK,
      ctxEffect: contextEffect,
      temp: temperature.toFixed(1),
      tempEffect: temperatureEffect,
    }),
  };
}

export function modalityBadgeMeta(modality?: string): {
  label: string;
  color: string;
  comfyOnly: boolean;
} {
  if (modality === "video") {
    return {
      label: t("models.compatModalityVideo"),
      color: "purple",
      comfyOnly: true,
    };
  }
  if (modality === "image") {
    return {
      label: t("models.compatModalityImage"),
      color: "magenta",
      comfyOnly: true,
    };
  }
  return {
    label: t("models.compatModalityText"),
    color: "blue",
    comfyOnly: false,
  };
}

export interface ColabOneDiskFit {
  insufficient: boolean;
  freeBytes: number;
  totalBytes: number;
  cachedBytes: number;
  remainingBytes: number;
  warning: string;
}

const COLABONE_DISK_MB = 1024 ** 2;
const COLABONE_DISK_GB = 1024 ** 3;

function formatColabOneDiskBytes(n: number, decimals = 1): string {
  const safe = Math.max(0, n);
  if (safe >= COLABONE_DISK_GB) {
    return `${(safe / COLABONE_DISK_GB).toFixed(decimals)} GB`;
  }
  return `${(safe / COLABONE_DISK_MB).toFixed(decimals === 1 ? 0 : 1)} MB`;
}

function resolveColabOneDiskFreeBytes(runtime: RuntimeStatus): number | null {
  const disk = runtime.resources?.disk;
  if (
    disk &&
    typeof disk.free_bytes === "number" &&
    Number.isFinite(disk.free_bytes) &&
    disk.free_bytes >= 0
  ) {
    if (
      disk.free_bytes > 0 ||
      (typeof disk.total_bytes === "number" &&
        Number.isFinite(disk.total_bytes) &&
        disk.total_bytes > 0)
    ) {
      return disk.free_bytes;
    }
  }
  if (
    typeof runtime.disk_free === "number" &&
    Number.isFinite(runtime.disk_free) &&
    runtime.disk_free > 0
  ) {
    return runtime.disk_free;
  }
  return null;
}

export function evaluateColabOneModelDiskFit(
  model: ModelSpec,
  runtime: RuntimeStatus,
): ColabOneDiskFit {
  const runningTask =
    runtime.state === "stopped"
      ? undefined
      : runtime.tasks?.find(
          (item) => item.state === "running" && item.model_id === model.id,
        );
  const downloadTask =
    runningTask?.kind === "download" ? runningTask : undefined;
  const taskTotal =
    downloadTask?.download &&
    typeof downloadTask.download.total_bytes === "number" &&
    Number.isFinite(downloadTask.download.total_bytes) &&
    downloadTask.download.total_bytes > 0
      ? downloadTask.download.total_bytes
      : 0;
  const totalBytes = taskTotal || (model.size > 0 ? model.size : 0);

  const isLoaded =
    runtime.state !== "stopped" &&
    !!(model.loaded || (!!model.id && runtime.loaded_model === model.id));

  if (
    runtime.state === "stopped" ||
    model.downloaded ||
    isLoaded ||
    runningTask?.kind === "load" ||
    totalBytes <= 0
  ) {
    return {
      insufficient: false,
      freeBytes: 0,
      totalBytes,
      cachedBytes: model.downloaded || isLoaded ? totalBytes : 0,
      remainingBytes: 0,
      warning: "",
    };
  }

  const serverCached =
    typeof model.cached_bytes === "number" &&
    Number.isFinite(model.cached_bytes) &&
    model.cached_bytes > 0
      ? model.cached_bytes
      : 0;

  let taskDownloaded = 0;
  if (downloadTask) {
    if (
      downloadTask.download &&
      typeof downloadTask.download.downloaded_bytes === "number" &&
      Number.isFinite(downloadTask.download.downloaded_bytes) &&
      downloadTask.download.downloaded_bytes > 0
    ) {
      taskDownloaded = downloadTask.download.downloaded_bytes;
    } else if (
      typeof downloadTask.progress === "number" &&
      Number.isFinite(downloadTask.progress) &&
      downloadTask.progress > 0
    ) {
      taskDownloaded = Math.round(
        Math.min(1, downloadTask.progress) * totalBytes,
      );
    }
  }

  const cachedBytes = Math.min(
    totalBytes,
    Math.max(serverCached, taskDownloaded),
  );
  const remainingBytes = Math.max(0, totalBytes - cachedBytes);
  const freeBytes = resolveColabOneDiskFreeBytes(runtime);

  if (
    freeBytes === null ||
    remainingBytes <= 0 ||
    freeBytes >= remainingBytes
  ) {
    return {
      insufficient: false,
      freeBytes: freeBytes ?? 0,
      totalBytes,
      cachedBytes,
      remainingBytes,
      warning: "",
    };
  }

  let freeLabel = formatColabOneDiskBytes(freeBytes);
  let needLabel = formatColabOneDiskBytes(remainingBytes);
  if (freeLabel === needLabel) {
    freeLabel = formatColabOneDiskBytes(freeBytes, 2);
    needLabel = formatColabOneDiskBytes(remainingBytes, 2);
  }

  const warning =
    cachedBytes >= COLABONE_DISK_MB
      ? t("models.warnDiskLowRemaining", {
          free: freeLabel,
          remaining: needLabel,
          cached: formatColabOneDiskBytes(cachedBytes),
        })
      : t("models.warnDiskLow", {
          free: freeLabel,
          size: needLabel,
        });

  return {
    insufficient: true,
    freeBytes,
    totalBytes,
    cachedBytes,
    remainingBytes,
    warning,
  };
}
