import { afterEach, describe, expect, it } from "vitest";
import {
  COLABONE_LOCALE_LIST,
  COLABONE_LOCALE_MAP,
  COLABONE_MESSAGES,
  colabOneAntdLocale,
  colabOneLocaleCode,
  colabOneLocaleIsManual,
  detectColabOneSystemLocale,
  hydrateColabOneLocaleFromSettings,
  normalizeColabOneLocale,
  setColabOneLocale,
  t,
  tb,
  tsrc,
  type ColabOneLocale,
  type ColabOneMessageKey,
} from "./i18n";
import { COLABONE_IDLE_RELEASE_OPTIONS, friendlyErrorMessage } from "./billing";
import { locationFull, locationShort, locationTooltip } from "./location";
import { describeColabOneRuntimeParams } from "./modelCompatibility";
import { COLABONE_STATUS_LABELS } from "./types";

const ALL_LOCALES: ColabOneLocale[] = [
  "zh-CN",
  "en-US",
  "zh-TW",
  "zh-HK",
  "ja-JP",
  "ko-KR",
];

afterEach(() => {
  setColabOneLocale("zh-CN", { persist: false, syncNative: false });
  colabOneLocaleIsManual.value = false;
});

describe("ColabOne i18n catalog integrity", () => {
  it("provides all 6 locales with ready-made flag-icons SVG URLs", () => {
    expect(COLABONE_LOCALE_LIST.map((item) => item.code)).toEqual(ALL_LOCALES);
    for (const meta of COLABONE_LOCALE_LIST) {
      expect(COLABONE_LOCALE_MAP[meta.code]).toEqual(meta);
      expect(meta.nativeLabel.length).toBeGreaterThan(0);
      expect(meta.flagUrl.length).toBeGreaterThan(0);
      // Vite resolves static SVG imports as either a data URI or a path ending in .svg
      expect(
        meta.flagUrl.startsWith("data:image/svg+xml") ||
          meta.flagUrl.endsWith(".svg"),
      ).toBe(true);
    }
    expect(COLABONE_LOCALE_MAP["zh-TW"].countryCode).toBe("TW");
    expect(COLABONE_LOCALE_MAP["zh-HK"].countryCode).toBe("HK");
    expect(COLABONE_LOCALE_MAP["ja-JP"].countryCode).toBe("JP");
    expect(COLABONE_LOCALE_MAP["ko-KR"].countryCode).toBe("KR");
  });

  it("maintains strict key and placeholder parity across all 6 locales", () => {
    const base = COLABONE_MESSAGES["zh-CN"];
    const baseKeys = Object.keys(base) as ColabOneMessageKey[];
    const placeholderRe = /\{(\w+)\}/g;

    for (const locale of ALL_LOCALES) {
      const cat = COLABONE_MESSAGES[locale];
      expect(Object.keys(cat).sort()).toEqual([...baseKeys].sort());
      for (const key of baseKeys) {
        const basePh = Array.from(base[key].matchAll(placeholderRe))
          .map((m) => m[1])
          .filter((name) => name !== "id")
          .sort();
        const locPh = Array.from(cat[key].matchAll(placeholderRe))
          .map((m) => m[1])
          .filter((name) => name !== "id")
          .sort();
        expect(locPh, `placeholder mismatch for ${locale}[${key}]`).toEqual(
          basePh,
        );
      }
    }
  });

  it("has zero CJK leaks in English/Korean and authentic Taiwan vs Hong Kong vocabulary", () => {
    const cjkRe = /[\u3400-\u9fff\uf900-\ufaff]/;
    for (const locale of ["en-US", "ko-KR"] as const) {
      for (const [key, val] of Object.entries(COLABONE_MESSAGES[locale])) {
        expect(cjkRe.test(val), `CJK leak in ${locale}[${key}]: ${val}`).toBe(
          false,
        );
      }
    }

    // Authentic Taiwan vs Hong Kong distinctions
    expect(COLABONE_MESSAGES["zh-TW"]["settings.threadsOption"]).toContain(
      "執行緒",
    );
    expect(COLABONE_MESSAGES["zh-HK"]["settings.threadsOption"]).toContain(
      "線程",
    );
    expect(COLABONE_MESSAGES["zh-TW"]["device.instancesHeading"]).toContain(
      "執行個體",
    );
    expect(COLABONE_MESSAGES["zh-HK"]["device.instancesHeading"]).toContain(
      "實例",
    );
    expect(COLABONE_MESSAGES["zh-TW"]["comfy.engineTitle"]).toContain(
      "工作流程",
    );
    expect(COLABONE_MESSAGES["zh-HK"]["comfy.engineTitle"]).toContain("工作流");
    expect(COLABONE_MESSAGES["zh-TW"]["settings.shortcutsTitle"]).toContain(
      "開始功能表",
    );
    expect(COLABONE_MESSAGES["zh-HK"]["settings.shortcutsTitle"]).toContain(
      "開始選單",
    );
  });
});

describe("ColabOne locale detection & manual-override priority", () => {
  it("normalizes BCP-47 tags accurately including Taiwan vs Hong Kong/Macau/Cantonese", () => {
    expect(normalizeColabOneLocale("zh-CN")).toBe("zh-CN");
    expect(normalizeColabOneLocale("zh_Hans_CN")).toBe("zh-CN");
    expect(normalizeColabOneLocale("zh-SG")).toBe("zh-CN");
    expect(normalizeColabOneLocale("zh-TW")).toBe("zh-TW");
    expect(normalizeColabOneLocale("zh-Hant")).toBe("zh-TW");
    expect(normalizeColabOneLocale("zh-Hant-TW")).toBe("zh-TW");
    expect(normalizeColabOneLocale("zh-HK")).toBe("zh-HK");
    expect(normalizeColabOneLocale("zh-MO")).toBe("zh-HK");
    expect(normalizeColabOneLocale("zh-Hant-HK")).toBe("zh-HK");
    expect(normalizeColabOneLocale("yue-Hant-HK")).toBe("zh-HK");
    expect(normalizeColabOneLocale("ja")).toBe("ja-JP");
    expect(normalizeColabOneLocale("ja-JP")).toBe("ja-JP");
    expect(normalizeColabOneLocale("ko")).toBe("ko-KR");
    expect(normalizeColabOneLocale("ko_KR")).toBe("ko-KR");
    expect(normalizeColabOneLocale("en")).toBe("en-US");
    expect(normalizeColabOneLocale("en-GB")).toBe("en-US");
    expect(normalizeColabOneLocale("fr-FR")).toBeNull();
  });

  it("auto-detects the first matching OS language and falls back to en-US for unsupported locales", () => {
    expect(detectColabOneSystemLocale(["fr-FR", "ja-JP", "en-US"])).toBe(
      "ja-JP",
    );
    expect(detectColabOneSystemLocale(["zh-HK", "en-US"])).toBe("zh-HK");
    expect(detectColabOneSystemLocale(["zh-TW", "zh-CN"])).toBe("zh-TW");
    expect(detectColabOneSystemLocale(["ko-KR"])).toBe("ko-KR");
    expect(detectColabOneSystemLocale(["de-DE", "es-ES"])).toBe("en-US");
  });

  it("preserves explicit manual selection and never lets settings hydration overwrite it", () => {
    setColabOneLocale("ja-JP", { persist: true, syncNative: false });
    expect(colabOneLocaleCode.value).toBe("ja-JP");
    expect(colabOneLocaleIsManual.value).toBe(true);
    expect(colabOneAntdLocale.value.locale).toBe("ja");

    // Settings from another default or stale snapshot must not overwrite manual choice
    hydrateColabOneLocaleFromSettings("en-US");
    expect(colabOneLocaleCode.value).toBe("ja-JP");

    // When no manual selection exists yet, saved settings hydrate cleanly
    colabOneLocaleIsManual.value = false;
    setColabOneLocale("zh-CN", { persist: false, syncNative: false });
    hydrateColabOneLocaleFromSettings("ko-KR");
    expect(colabOneLocaleCode.value).toBe("ko-KR");
    expect(colabOneLocaleIsManual.value).toBe(true);
  });
});

describe("ColabOne reactive translations across helpers", () => {
  it("updates reactive exports, backend message translation, and locations per locale", () => {
    setColabOneLocale("en-US", { persist: false, syncNative: false });
    expect(COLABONE_STATUS_LABELS.ready).toBe("Connected");
    expect(t("appearance.system")).toBe("System");
    expect(COLABONE_IDLE_RELEASE_OPTIONS[1].label).toContain("30 min");
    expect(tsrc("远端 GPU")).toBe("Remote GPU");
    expect(tb("远端 GPU 已连接")).toBe("Remote GPU connected");
    expect(tb("客户端已启动")).toBe("ColabOne client started");
    expect(tb("31B 稠密 / Q8_0")).toContain("Dense");
    expect(
      locationShort({ country: "US", country_name: "美国", region: "俄勒冈" }),
    ).toBe("United States");
    expect(
      locationFull({ country: "US", country_name: "美国", region: "俄勒冈" }),
    ).toBe("United States · Oregon");
    expect(
      locationTooltip({
        country: "US",
        country_name: "美国",
        region: "俄勒冈",
        zone: "us-west1-b",
      }),
    ).toContain("Instance location:");

    setColabOneLocale("ja-JP", { persist: false, syncNative: false });
    expect(COLABONE_STATUS_LABELS.ready).toBe("接続済み");
    expect(t("nav.device")).toBe("リモートデバイス");
    expect(tsrc("计费")).toBe("課金");
    expect(
      locationFull({ country: "JP", country_name: "日本", region: "东京" }),
    ).toBe("日本 · 東京");

    setColabOneLocale("ko-KR", { persist: false, syncNative: false });
    expect(COLABONE_STATUS_LABELS.ready).toBe("연결됨");
    expect(t("nav.settings")).toBe("설정");
    expect(tsrc("对外 API")).toBe("외부 API");
    expect(
      locationFull({ country: "KR", country_name: "韩国", region: "首尔" }),
    ).toBe("대한민국 · 서울");
  });

  it("strips error prefixes, translates runtime/hybrid backend logs, and avoids fullwidth parens in non-CJK locales", () => {
    const cjkRe = /[\u3400-\u9fff\uf900-\ufaff（）「」]/;
    const samples = [
      "Error: 请先完成 Google 登录",
      "RuntimeError: 启动 llama.cpp：GPU 显存 + 系统内存智能混合装配（--fit on · KV q8_0）",
      "启动 llama.cpp：GPU + 内存 MoE 混合装配（--fit on · 24 层专家内存卸载 · KV q8_0）",
      "模型权重已就绪（全量 GPU 驻留（FlashAttn · --fit on · KV q8_0），耗时 12.4 秒）",
      "方案「GPU + 内存智能混合装配（--fit on · 紧凑 KV q4_0）」未启动成功，自动尝试下一降级装载方案…",
      "模型体积大于当前显存，已自动启用 GPU 显存 + 系统内存混合装载（--fit on），溢出权重将自动分配至内存",
      "远端设备 NVIDIA T4，显存 15.0 GiB，模型权重 22.3 GiB",
      "正在重新装载 Qwen3-32B，先卸载当前实例以释放显存",
      "模型加载成功，远端推理服务已就绪",
    ];

    for (const locale of ["en-US", "ko-KR"] as const) {
      setColabOneLocale(locale, { persist: false, syncNative: false });
      for (const s of samples) {
        const out = tb(s);
        expect(cjkRe.test(out), `CJK/fullwidth leak in ${locale}: ${out}`).toBe(
          false,
        );
      }
      expect(
        cjkRe.test(friendlyErrorMessage("Error: 请先完成 Google 登录")),
      ).toBe(false);
      const paramDesc = describeColabOneRuntimeParams(8192, 0.7);
      for (const field of [
        paramDesc.presetTitle,
        paramDesc.contextEffect,
        paramDesc.temperatureEffect,
        paramDesc.combinedEffect,
      ]) {
        expect(
          cjkRe.test(field),
          `fullwidth/CJK leak in ${locale} runtime params: ${field}`,
        ).toBe(false);
      }
    }

    setColabOneLocale("en-US", { persist: false, syncNative: false });
    expect(friendlyErrorMessage("Error: 请先完成 Google 登录")).toBe(
      "Please sign in with Google first",
    );
    expect(describeColabOneRuntimeParams(8192, 0.7).presetTitle).toContain(
      "(Recommended)",
    );
  });
});
