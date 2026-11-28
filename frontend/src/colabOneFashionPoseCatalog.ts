import type { ColabOneMessageKey } from "./i18n/locales/zh-CN";

export const COLABONE_FASHION_MAX_POSE_SHOTS = 12;
export const COLABONE_FASHION_MAX_POSE_REFS = 6;
export const COLABONE_FASHION_POSE_SHOTS_KEY = "pipeline.pose_shots";

export const COLABONE_FASHION_POSE_REF_SLOT_KEYS = [
  "pipeline.pose_ref_1",
  "pipeline.pose_ref_2",
  "pipeline.pose_ref_3",
  "pipeline.pose_ref_4",
  "pipeline.pose_ref_5",
  "pipeline.pose_ref_6",
] as const;

export const COLABONE_FASHION_POSE_VIEW_IDS = [
  "auto",
  "front",
  "side",
  "back",
  "detail",
] as const;

export type ColabOneFashionPoseShotView =
  (typeof COLABONE_FASHION_POSE_VIEW_IDS)[number];

export const COLABONE_FASHION_POSE_CATEGORIES = [
  "all",
  "front",
  "side",
  "back",
  "dynamic",
  "seated",
  "detail",
  "kids",
] as const;

export type ColabOneFashionPoseCategoryId =
  (typeof COLABONE_FASHION_POSE_CATEGORIES)[number];

export type ColabOneFashionPosePackId = "full_6" | "core_4" | "kids_4" | "hero_1";

export interface ColabOneFashionPoseSvgCircle {
  cx: number;
  cy: number;
  r: number;
  dashed?: boolean;
}

export interface ColabOneFashionPoseSvgPath {
  d: string;
  dashed?: boolean;
  muted?: boolean;
}

export interface ColabOneFashionPoseSvgRect {
  x: number;
  y: number;
  width: number;
  height: number;
  rx?: number;
  dashed?: boolean;
  muted?: boolean;
}

export interface ColabOneFashionPoseSvgSpec {
  circles: readonly ColabOneFashionPoseSvgCircle[];
  paths: readonly ColabOneFashionPoseSvgPath[];
  rects?: readonly ColabOneFashionPoseSvgRect[];
}

export interface ColabOneFashionPosePreset {
  id: string;
  category: Exclude<ColabOneFashionPoseCategoryId, "all">;
  view: Exclude<ColabOneFashionPoseShotView, "auto">;
  labelKey: ColabOneMessageKey;
  fallbackLabel: string;
  svg: ColabOneFashionPoseSvgSpec;
}

export type ColabOneFashionPoseShot =
  | {
      kind: "preset";
      preset_id: string;
    }
  | {
      kind: "reference";
      slot_key: string;
      view: ColabOneFashionPoseShotView;
      note: string;
      borrow_scene_lighting: boolean;
    }
  | {
      kind: "text";
      prompt: string;
      view: ColabOneFashionPoseShotView;
    };

export const COLABONE_FASHION_POSE_PACKS: ReadonlyArray<{
  id: ColabOneFashionPosePackId;
  labelKey: ColabOneMessageKey;
  presetIds: readonly string[];
}> = [
  {
    id: "full_6",
    labelKey: "comfy.fashionPosePackFull6",
    presetIds: ["pose_1", "pose_2", "pose_3", "pose_4", "pose_5", "pose_6"],
  },
  {
    id: "core_4",
    labelKey: "comfy.fashionPosePackCore4",
    presetIds: ["pose_1", "pose_2", "pose_3", "pose_5"],
  },
  {
    id: "kids_4",
    labelKey: "comfy.fashionPosePackKids4",
    presetIds: ["pose_27", "pose_28", "pose_29", "pose_30"],
  },
  {
    id: "hero_1",
    labelKey: "comfy.fashionPosePackHero1",
    presetIds: ["pose_1"],
  },
];

export const COLABONE_FASHION_POSE_PRESETS: readonly ColabOneFashionPosePreset[] = [
  // Front (4)
  {
    id: "pose_1",
    category: "front",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_1",
    fallbackLabel: "正面标准站姿（主图）",
    svg: {
      circles: [{ cx: 24, cy: 9, r: 4 }],
      paths: [
        { d: "M17 17c2-2 12-2 14 0l2 14M17 17l-2 14" },
        { d: "M20 17l-1 15h10l-1-15M21 32l-1 19M27 32l1 19" },
      ],
    },
  },
  {
    id: "pose_7",
    category: "front",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_7",
    fallbackLabel: "微侧重心自然站",
    svg: {
      circles: [{ cx: 25, cy: 9, r: 4 }],
      paths: [
        { d: "M18 17c3-2 11-1.5 13 0.5l2 13M18 17l-3 13" },
        { d: "M20 17l-2 15h10l1-14M21 32l-2 19M26 32l4 18" },
      ],
    },
  },
  {
    id: "pose_8",
    category: "front",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_8",
    fallbackLabel: "双手轻叠正面站",
    svg: {
      circles: [{ cx: 24, cy: 9, r: 4 }],
      paths: [
        { d: "M17 17c2-2 12-2 14 0l3 8-10 4M17 17l-3 8 10 4" },
        { d: "M20 17l-1 15h10l-1-15M21 32l-0.5 19M27 32l0.5 19" },
      ],
    },
  },
  {
    id: "pose_9",
    category: "front",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_9",
    fallbackLabel: "微仰视角修长正面",
    svg: {
      circles: [{ cx: 24, cy: 8, r: 3.6 }],
      paths: [
        { d: "M18 15c2-1.8 10-1.8 12 0l2 14M18 15l-2 14" },
        { d: "M20 15l-1.5 15h11L28 15M21 30l-2 22M27 30l2 22" },
        { d: "M13 52h22", muted: true, dashed: true },
      ],
    },
  },

  // Side (5)
  {
    id: "pose_3",
    category: "side",
    view: "side",
    labelKey: "comfy.fashionPosePreset_pose_3",
    fallbackLabel: "左侧微侧微笑",
    svg: {
      circles: [{ cx: 26, cy: 9, r: 4 }],
      paths: [
        { d: "M20 17c3-2 9-2 11 1l2 13M20 17l-3 12 4 3" },
        { d: "M21 18l-2 14h9l1-14M22 32l-2 19M26 32l4 19" },
      ],
    },
  },
  {
    id: "pose_10",
    category: "side",
    view: "side",
    labelKey: "comfy.fashionPosePreset_pose_10",
    fallbackLabel: "45° 立体侧身展示",
    svg: {
      circles: [{ cx: 27, cy: 9, r: 4 }],
      paths: [
        { d: "M21 17c3-2 8-1 10 2l3 11M21 17l-2 13" },
        { d: "M22 18l-1 14h7l2-14M23 32l-4 19M27 32l3 19" },
      ],
    },
  },
  {
    id: "pose_11",
    category: "side",
    view: "side",
    labelKey: "comfy.fashionPosePreset_pose_11",
    fallbackLabel: "右侧微侧自然看镜",
    svg: {
      circles: [{ cx: 22, cy: 9, r: 4 }],
      paths: [
        { d: "M17 18c2-3 8-3 11-1l3 12-4 3M17 18l-2 13" },
        { d: "M19 18l1 14h9l-2-14M22 32l-4 19M26 32l2 19" },
      ],
    },
  },
  {
    id: "pose_12",
    category: "side",
    view: "side",
    labelKey: "comfy.fashionPosePreset_pose_12",
    fallbackLabel: "单手插兜侧身松弛",
    svg: {
      circles: [{ cx: 25, cy: 9, r: 4 }],
      paths: [
        { d: "M19 17c3-2 9-1.5 11 1l4 7-5 6M19 17l-3 14" },
        { d: "M20 18l-1 14h9l1-14M22 32l-3 19M26 32l3 19" },
      ],
    },
  },
  {
    id: "pose_13",
    category: "side",
    view: "side",
    labelKey: "comfy.fashionPosePreset_pose_13",
    fallbackLabel: "轻扶领口侧身展示",
    svg: {
      circles: [{ cx: 25, cy: 9, r: 4 }],
      paths: [
        { d: "M19 17c3-2 9-1.5 11 1l4 7-7-5M19 17l-3 14" },
        { d: "M20 18l-1 14h9l1-14M22 32l-2 19M26 32l3 19" },
      ],
    },
  },

  // Back & Over-shoulder (4)
  {
    id: "pose_6",
    category: "back",
    view: "back",
    labelKey: "comfy.fashionPosePreset_pose_6",
    fallbackLabel: "完整背面展示",
    svg: {
      circles: [{ cx: 24, cy: 9, r: 4, dashed: true }],
      paths: [
        { d: "M17 17c3-2 11-2 14 0l2 15M17 17l-2 15" },
        { d: "M19 17v15h10V17M22 32v19M26 32v19" },
        { d: "M21 22h6", muted: true },
      ],
    },
  },
  {
    id: "pose_4",
    category: "back",
    view: "side",
    labelKey: "comfy.fashionPosePreset_pose_4",
    fallbackLabel: "右侧优雅回眸",
    svg: {
      circles: [{ cx: 22, cy: 9, r: 4 }],
      paths: [
        { d: "M26 9h3", dashed: true, muted: true },
        { d: "M19 17c4-2 10-1 12 1l2 13M19 17l-3 14" },
        { d: "M20 18l1 14h9l-1-14M23 32l-2 19M28 32l2 19" },
      ],
    },
  },
  {
    id: "pose_14",
    category: "back",
    view: "back",
    labelKey: "comfy.fashionPosePreset_pose_14",
    fallbackLabel: "左侧背影轻转回眸",
    svg: {
      circles: [{ cx: 26, cy: 9, r: 4, dashed: true }],
      paths: [
        { d: "M19 9h3", dashed: true, muted: true },
        { d: "M17 18c2-2 8-3 12-1l3 14M17 18l-2 13" },
        { d: "M19 18l-1 14h9l1-14M20 32l-2 19M25 32l2 19" },
      ],
    },
  },
  {
    id: "pose_15",
    category: "back",
    view: "back",
    labelKey: "comfy.fashionPosePreset_pose_15",
    fallbackLabel: "背面展开裙摆 / 披风",
    svg: {
      circles: [{ cx: 24, cy: 9, r: 4, dashed: true }],
      paths: [
        { d: "M18 17c2-2 10-2 12 0M18 17l-7 14 4 3M30 17l7 14-4 3" },
        { d: "M20 17l-6 19h20l-6-19M21 36v15M27 36v15" },
        { d: "M21 22h6", muted: true },
      ],
    },
  },

  // Dynamic & Interactive (5)
  {
    id: "pose_2",
    category: "dynamic",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_2",
    fallbackLabel: "轻提裙摆 / 自然互动",
    svg: {
      circles: [{ cx: 24, cy: 9, r: 4 }],
      paths: [
        { d: "M18 17c2-2 10-2 12 0M18 17l-7 13 4 4M30 17l7 13-4 4" },
        { d: "M20 17l-5 19h18l-5-19M21 36v15M27 36v15" },
      ],
    },
  },
  {
    id: "pose_16",
    category: "dynamic",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_16",
    fallbackLabel: "自然迈步迎面抓拍",
    svg: {
      circles: [{ cx: 24, cy: 9, r: 4 }],
      paths: [
        { d: "M18 17c2-2 10-2 12 0l5 12M18 17l-5 11" },
        { d: "M20 17l-1 15h10l-1-15M22 32l-6 19M27 32l6 18" },
      ],
    },
  },
  {
    id: "pose_17",
    category: "dynamic",
    view: "side",
    labelKey: "comfy.fashionPosePreset_pose_17",
    fallbackLabel: "轻盈转身裙摆飞扬",
    svg: {
      circles: [{ cx: 24, cy: 9, r: 4 }],
      paths: [
        { d: "M18 17c2-2 10-2 12 0l9 6M18 17l-9 7" },
        { d: "M20 17l-6 18c6 3 14 3 20 0l-6-18M22 36l-3 15M27 36l4 15" },
      ],
    },
  },
  {
    id: "pose_18",
    category: "dynamic",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_18",
    fallbackLabel: "双手插兜随性漫步",
    svg: {
      circles: [{ cx: 24, cy: 9, r: 4 }],
      paths: [
        { d: "M17 17c2-2 12-2 14 0l4 8-5 6M17 17l-4 8 5 6" },
        { d: "M20 17l-1 15h10l-1-15M22 32l-5 19M27 32l5 18" },
      ],
    },
  },
  {
    id: "pose_19",
    category: "dynamic",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_19",
    fallbackLabel: "轻撩发丝自然动态",
    svg: {
      circles: [{ cx: 24, cy: 9, r: 4 }],
      paths: [
        { d: "M18 17c2-2 10-2 12 0l5-6-6-2M18 17l-3 14" },
        { d: "M20 17l-1 15h10l-1-15M21 32l-1 19M27 32l2 19" },
      ],
    },
  },

  // Seated & Leaning (4)
  {
    id: "pose_20",
    category: "seated",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_20",
    fallbackLabel: "极简高脚凳画报坐姿",
    svg: {
      circles: [{ cx: 23, cy: 10, r: 4 }],
      paths: [
        { d: "M18 18c2-2 9-2 11 0l3 11M18 18l-3 11" },
        { d: "M19 18l1 13h8l-1-13M20 31l10 2 3 16M24 31l-4 18" },
        { d: "M15 34h14M18 34v16M26 34v16", muted: true },
      ],
    },
  },
  {
    id: "pose_21",
    category: "seated",
    view: "side",
    labelKey: "comfy.fashionPosePreset_pose_21",
    fallbackLabel: "低凳 / 台阶自然斜坐",
    svg: {
      circles: [{ cx: 22, cy: 13, r: 4 }],
      paths: [
        { d: "M17 21c2-2 8-2 10 0l4 10M17 21l-3 11" },
        { d: "M18 21l1 13h8l-1-13M20 34l11 3 3 13M24 34l4 16" },
        { d: "M12 37h15v13H12z", muted: true },
      ],
    },
  },
  {
    id: "pose_22",
    category: "seated",
    view: "side",
    labelKey: "comfy.fashionPosePreset_pose_22",
    fallbackLabel: "轻倚墙面松弛站姿",
    svg: {
      circles: [{ cx: 23, cy: 10, r: 4 }],
      paths: [
        { d: "M11 6v46", muted: true },
        { d: "M18 18c2-2 10-1 11 1l2 12M18 18l-4 10 4 4" },
        { d: "M19 18l2 14h8l-2-14M22 32l-2 19M26 32l6 18" },
      ],
    },
  },
  {
    id: "pose_23",
    category: "seated",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_23",
    fallbackLabel: "单手轻托下颌静坐",
    svg: {
      circles: [{ cx: 24, cy: 12, r: 4.2 }],
      paths: [
        { d: "M18 20c2-2 10-2 12 0l4 10-7-11M18 20l-3 12" },
        { d: "M19 20l1 14h8l1-14M20 34l-3 14M28 34l3 14" },
        { d: "M14 37h20", muted: true },
      ],
    },
  },

  // Half-body & Detail (4)
  {
    id: "pose_5",
    category: "detail",
    view: "detail",
    labelKey: "comfy.fashionPosePreset_pose_5",
    fallbackLabel: "半身工艺细节特写",
    svg: {
      rects: [{ x: 7, y: 4, width: 34, height: 46, rx: 4, dashed: true, muted: true }],
      circles: [{ cx: 24, cy: 17, r: 6 }],
      paths: [{ d: "M13 33c3-6 19-6 22 0v14H13z" }],
    },
  },
  {
    id: "pose_24",
    category: "detail",
    view: "detail",
    labelKey: "comfy.fashionPosePreset_pose_24",
    fallbackLabel: "七分身穿搭近景",
    svg: {
      rects: [{ x: 7, y: 4, width: 34, height: 46, rx: 4, dashed: true, muted: true }],
      circles: [{ cx: 24, cy: 13, r: 4.6 }],
      paths: [
        { d: "M16 22c3-2.5 13-2.5 16 0l3 14M16 22l-3 14" },
        { d: "M19 22l-1 18h12l-1-18M21 40v8M27 40v8" },
      ],
    },
  },
  {
    id: "pose_25",
    category: "detail",
    view: "detail",
    labelKey: "comfy.fashionPosePreset_pose_25",
    fallbackLabel: "微侧半身领口特写",
    svg: {
      rects: [{ x: 7, y: 4, width: 34, height: 46, rx: 4, dashed: true, muted: true }],
      circles: [{ cx: 26, cy: 17, r: 5.8 }],
      paths: [{ d: "M14 33c4-6 17-5 21 1v13H14zM20 31l5 4 4-3" }],
    },
  },
  {
    id: "pose_26",
    category: "detail",
    view: "detail",
    labelKey: "comfy.fashionPosePreset_pose_26",
    fallbackLabel: "轻抚衣襟 / 袖口细节",
    svg: {
      rects: [{ x: 7, y: 4, width: 34, height: 46, rx: 4, dashed: true, muted: true }],
      circles: [{ cx: 24, cy: 16, r: 5.5 }],
      paths: [{ d: "M14 31c3-5 17-5 20 0v16H14zM18 39l5-10" }],
    },
  },

  // Kids Lively (4)
  {
    id: "pose_27",
    category: "kids",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_27",
    fallbackLabel: "开心踮脚提裙摆",
    svg: {
      circles: [{ cx: 24, cy: 13, r: 4.5 }],
      paths: [
        { d: "M18 21c2-1.5 10-1.5 12 0M18 21l-7 10 4 3M30 21l7 10-4 3" },
        { d: "M20 21l-5 16h18l-5-16M21 37l-2 13M27 37l2 13" },
      ],
    },
  },
  {
    id: "pose_28",
    category: "kids",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_28",
    fallbackLabel: "俏皮挥手甜美微笑",
    svg: {
      circles: [{ cx: 24, cy: 13, r: 4.5 }],
      paths: [
        { d: "M18 21c2-1.5 10-1.5 12 0M18 21l-5 12M30 21l7-9" },
        { d: "M20 21l-2 15h12l-2-15M21 36l-1 14M27 36l1 14" },
      ],
    },
  },
  {
    id: "pose_29",
    category: "kids",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_29",
    fallbackLabel: "歪头甜笑双手背后",
    svg: {
      circles: [{ cx: 25, cy: 13, r: 4.5 }],
      paths: [
        { d: "M18 21c2-1.5 10-1.5 12 0M18 21l-3 9 3 4M30 21l3 9-3 4" },
        { d: "M20 21l-1 15h10l-1-15M22 36l-1 14M26 36l2 14" },
      ],
    },
  },
  {
    id: "pose_30",
    category: "kids",
    view: "front",
    labelKey: "comfy.fashionPosePreset_pose_30",
    fallbackLabel: "轻快小步跳跃抓拍",
    svg: {
      circles: [{ cx: 24, cy: 11, r: 4.5 }],
      paths: [
        { d: "M18 19c2-1.5 10-1.5 12 0l8 5M18 19l-8 5" },
        { d: "M20 19l-4 15h16l-4-15M21 34l-5 12M27 34l5 12" },
        { d: "M15 50h18", muted: true, dashed: true },
      ],
    },
  },
];

export const COLABONE_FASHION_POSE_PRESET_BY_ID: ReadonlyMap<
  string,
  ColabOneFashionPosePreset
> = new Map(COLABONE_FASHION_POSE_PRESETS.map((p) => [p.id, p]));

const ALLOWED_POSE_REF_SLOTS = new Set<string>(COLABONE_FASHION_POSE_REF_SLOT_KEYS);
const ALLOWED_POSE_VIEWS = new Set<string>(COLABONE_FASHION_POSE_VIEW_IDS);

function parseBoolLite(val: unknown, fallback: boolean): boolean {
  if (typeof val === "boolean") return val;
  if (typeof val === "number") return val !== 0;
  if (typeof val === "string") {
    const low = val.trim().toLowerCase();
    if (low === "true" || low === "1" || low === "yes" || low === "on") return true;
    if (low === "false" || low === "0" || low === "no" || low === "off") return false;
  }
  return fallback;
}

export function inferColabOneFashionPoseView(
  text: string,
  explicitView?: string,
): ColabOneFashionPoseShotView {
  const ev = String(explicitView || "auto").trim().toLowerCase();
  if (ev === "front" || ev === "side" || ev === "back" || ev === "detail") {
    return ev;
  }
  const low = String(text || "").toLowerCase();
  if (
    /(背面|后背|後背|背影|后视|後視)/.test(low) ||
    /(?:^|[^a-z])(?:back|rear)(?:$|[^a-z])/.test(low)
  ) {
    return "back";
  }
  if (
    /(特写|特寫|近景|半身|七分|微距)/.test(low) ||
    /(?:^|[^a-z])(?:closeup|close-up|detail|macro)(?:$|[^a-z])/.test(low)
  ) {
    return "detail";
  }
  if (
    /(微侧|微側|侧身|側身|侧面|側面|回眸)/.test(low) ||
    /(?:^|[^a-z])(?:side|profile|three-quarter)(?:$|[^a-z])/.test(low)
  ) {
    return "side";
  }
  return "front";
}

export function sanitizeColabOneFashionPoseShot(
  raw: unknown,
): ColabOneFashionPoseShot | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const rec = raw as Record<string, unknown>;
  const kind = typeof rec.kind === "string" ? rec.kind.trim().toLowerCase() : "";
  if (kind === "preset") {
    const rawPreset =
      typeof rec.preset_id === "string"
        ? rec.preset_id
        : typeof rec.id === "string"
          ? rec.id
          : "";
    const presetId = rawPreset.trim();
    if (!COLABONE_FASHION_POSE_PRESET_BY_ID.has(presetId)) {
      return null;
    }
    return { kind: "preset", preset_id: presetId };
  }
  if (kind === "reference") {
    const slotKey = typeof rec.slot_key === "string" ? rec.slot_key.trim() : "";
    if (!ALLOWED_POSE_REF_SLOTS.has(slotKey)) {
      return null;
    }
    const rawView = typeof rec.view === "string" ? rec.view.trim().toLowerCase() : "auto";
    const view: ColabOneFashionPoseShotView = ALLOWED_POSE_VIEWS.has(rawView)
      ? (rawView as ColabOneFashionPoseShotView)
      : "auto";
    // Preserve internal/trailing spaces while typing; backend and blur handlers trim.
    const note = (typeof rec.note === "string" ? rec.note : "")
      .replace(/[\r\n]+/g, " ")
      .slice(0, 500);
    const borrow_scene_lighting = parseBoolLite(rec.borrow_scene_lighting, false);
    return {
      kind: "reference",
      slot_key: slotKey,
      view,
      note,
      borrow_scene_lighting,
    };
  }
  if (kind === "text") {
    const prompt = (typeof rec.prompt === "string" ? rec.prompt : "")
      .trim()
      .slice(0, 2000);
    if (!prompt) {
      return null;
    }
    const rawView = typeof rec.view === "string" ? rec.view.trim().toLowerCase() : "auto";
    const view: ColabOneFashionPoseShotView = ALLOWED_POSE_VIEWS.has(rawView)
      ? (rawView as ColabOneFashionPoseShotView)
      : "auto";
    return { kind: "text", prompt, view };
  }
  return null;
}

/**
 * Parse structured `pipeline.pose_shots`.
 * Returns `null` when unset/unparseable (meaning fall back to legacy `shot_pack` / `custom_pose_prompts`),
 * or an explicit array (including `[]` when the user cleared the queue).
 */
export function parseColabOneFashionPoseShots(
  raw: unknown,
): ColabOneFashionPoseShot[] | null {
  let parsed: unknown = raw;
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (!trimmed) {
      return null;
    }
    try {
      parsed = JSON.parse(trimmed);
    } catch {
      return null;
    }
  }
  if (!Array.isArray(parsed)) {
    return null;
  }
  const out: ColabOneFashionPoseShot[] = [];
  for (const item of parsed) {
    if (out.length >= COLABONE_FASHION_MAX_POSE_SHOTS) break;
    const clean = sanitizeColabOneFashionPoseShot(item);
    if (clean) {
      out.push(clean);
    }
  }
  return out;
}

export function serializeColabOneFashionPoseShots(
  shots: readonly ColabOneFashionPoseShot[],
): string {
  const clean: ColabOneFashionPoseShot[] = [];
  for (const item of shots) {
    if (clean.length >= COLABONE_FASHION_MAX_POSE_SHOTS) break;
    const s = sanitizeColabOneFashionPoseShot(item);
    if (s) {
      clean.push(s);
    }
  }
  return JSON.stringify(clean);
}

export function buildColabOneFashionPackShots(
  packId: ColabOneFashionPosePackId,
): ColabOneFashionPoseShot[] {
  const pack =
    COLABONE_FASHION_POSE_PACKS.find((p) => p.id === packId) ||
    COLABONE_FASHION_POSE_PACKS[0];
  return pack.presetIds.map((preset_id) => ({ kind: "preset", preset_id }));
}

export function resolveColabOneFashionPoseShotsFromValues(
  values: Record<string, string | number | boolean> | undefined | null,
  activePoseRefSlotKeys?: readonly string[] | ReadonlySet<string>,
): ColabOneFashionPoseShot[] {
  const hasOwn = (k: string) =>
    Boolean(values) && Object.prototype.hasOwnProperty.call(values, k);
  const rawStructured = hasOwn(COLABONE_FASHION_POSE_SHOTS_KEY)
    ? values?.[COLABONE_FASHION_POSE_SHOTS_KEY]
    : undefined;
  const parsedStructured = parseColabOneFashionPoseShots(rawStructured);
  if (parsedStructured !== null) {
    if (!activePoseRefSlotKeys) {
      return parsedStructured;
    }
    const activeSet =
      activePoseRefSlotKeys instanceof Set
        ? activePoseRefSlotKeys
        : new Set<string>(activePoseRefSlotKeys);
    return parsedStructured.filter(
      (shot) => shot.kind !== "reference" || activeSet.has(shot.slot_key),
    );
  }

  // Legacy fallback: custom_pose_prompts > shot_pack + pose_1..6_enabled
  const customRaw = hasOwn("pipeline.custom_pose_prompts")
    ? String(values?.["pipeline.custom_pose_prompts"] ?? "").trim()
    : "";
  if (customRaw) {
    const lines = customRaw
      .split(/\r?\n/)
      .map((l) => l.trim())
      .filter(Boolean)
      .slice(0, COLABONE_FASHION_MAX_POSE_SHOTS);
    if (lines.length > 0) {
      return lines.map((prompt) => ({
        kind: "text",
        prompt,
        view: inferColabOneFashionPoseView(prompt, "auto"),
      }));
    }
  }

  const packRaw = hasOwn("pipeline.shot_pack")
    ? String(values?.["pipeline.shot_pack"] ?? "full_6")
        .trim()
        .toLowerCase()
    : "full_6";
  if (packRaw === "hero_1") return buildColabOneFashionPackShots("hero_1");
  if (packRaw === "core_4") return buildColabOneFashionPackShots("core_4");
  if (packRaw === "kids_4") return buildColabOneFashionPackShots("kids_4");
  if (packRaw === "custom") {
    const selected: ColabOneFashionPoseShot[] = [];
    for (let i = 1; i <= 6; i += 1) {
      const key = `pipeline.pose_${i}_enabled`;
      const enabled = hasOwn(key) ? parseBoolLite(values?.[key], true) : true;
      if (enabled) {
        selected.push({ kind: "preset", preset_id: `pose_${i}` });
      }
    }
    return selected;
  }
  return buildColabOneFashionPackShots("full_6");
}

export function pruneColabOneFashionPoseShotsForMedia(
  rawPoseShots: unknown,
  validPoseRefSlotKeys: readonly string[] | ReadonlySet<string>,
): string {
  const parsed = parseColabOneFashionPoseShots(rawPoseShots);
  if (parsed === null) {
    return "";
  }
  const validSet =
    validPoseRefSlotKeys instanceof Set
      ? validPoseRefSlotKeys
      : new Set<string>(validPoseRefSlotKeys);
  const kept = parsed.filter(
    (shot) => shot.kind !== "reference" || validSet.has(shot.slot_key),
  );
  // If every single item was a stale reference photo that is no longer persisted,
  // reset to "" so opening a text-only draft gracefully falls back to the default pack.
  if (kept.length === 0 && parsed.length > 0 && parsed.every((s) => s.kind === "reference")) {
    return "";
  }
  return serializeColabOneFashionPoseShots(kept);
}
