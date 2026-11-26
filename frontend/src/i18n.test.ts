import { afterEach, describe, expect, it } from "vitest";
import comfyCatalog from "../../catalog/comfyui.json";
import {
  COLABONE_ANTD_LOCALES,
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
  translateColabOneBackendText,
  tsrc,
  type ColabOneLocale,
  type ColabOneMessageKey,
} from "./i18n";
import { COLABONE_IDLE_RELEASE_OPTIONS, friendlyErrorMessage } from "./billing";
import { EXACT_MESSAGES, REDACTION_TOKENS, SOURCE_LABELS } from "./i18n/backendExactMessages";
import { COUNTRY_OVERRIDES_BY_LOCALE, SUBREGION_TRANSLATIONS } from "./i18n/locations";
import { locationFull, locationShort, locationTooltip } from "./location";
import {
  buildColabOneGpuTargetOptions,
  describeColabOneRuntimeParams,
  planColabOneVersionOptions,
} from "./modelCompatibility";
import { filterCommonModels } from "./modelLibrary";
import { COLABONE_STATUS_LABELS, type ModelSpec, type RuntimeStatus } from "./types";

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

  it("uses unified playful empty-data copy ('空空如也' and localized equivalents) across Ant Design and app empty states", () => {
    const emptyKeys: ColabOneMessageKey[] = [
      "common.emptyData",
      "cuUsage.kpiPeriodConsumedSubEmpty",
      "cuUsage.kpiPeakSubEmpty",
      "cuUsage.emptyNoDataDay",
      "cuUsage.emptyCollectingHint",
      "device.releaseEmpty",
      "api.speedEmpty",
      "api.reqReadyEmpty",
      "models.emptySearch",
      "models.emptyMine",
      "models.hubEmpty",
      "models.pickerNoGguf",
      "comfy.emptyWorkflows",
      "comfy.emptyOutputs",
      "logs.emptyError",
      "logs.emptyAll",
    ];
    const expectedStemByLocale: Record<ColabOneLocale, string> = {
      "zh-CN": "空空如也",
      "zh-TW": "空空如也",
      "zh-HK": "空空如也",
      "en-US": "All empty here",
      "ja-JP": "空っぽ",
      "ko-KR": "텅 비었어요",
    };

    for (const locale of ALL_LOCALES) {
      const expected = COLABONE_MESSAGES[locale]["common.emptyData"];
      expect(COLABONE_ANTD_LOCALES[locale].Empty?.description).toBe(expected);
      expect(COLABONE_ANTD_LOCALES[locale].Table?.emptyText).toBe(expected);
      for (const key of emptyKeys) {
        expect(
          COLABONE_MESSAGES[locale][key],
          `${locale}[${key}] should express unified empty-data copy`,
        ).toContain(expectedStemByLocale[locale]);
      }
    }
  });

  it("forbids right-arrow symbols across all locale strings and exact backend messages and keeps digital human input/output split", () => {
    const RIGHT_ARROW_RE = new RegExp(
      "[\\u2192\\u219d\\u21a0\\u21a3\\u21a6\\u21aa\\u21ac\\u21b3\\u21c0\\u21c1\\u21c9\\u21d2\\u21db\\u21dd\\u21e2\\u21e5\\u21e8\\u21f6\\u21f8\\u21fb\\u21fe\\u2794\\u2799-\\u279e\\u27a0-\\u27a4\\u27f6\\u27f9\\u27fc\\u27fe\\u27ff\\u2b95]|" +
        "&ra" +
        "rr;|&#85" +
        "94;|&#x21" +
        "92;",
      "i",
    );

    for (const loc of ALL_LOCALES) {
      const table = COLABONE_MESSAGES[loc];
      expect(table["comfy.ioInputDigitalHuman"]).toBeTruthy();
      expect(table["comfy.ioOutputDigitalHuman"]).toBeTruthy();
      for (const [key, value] of Object.entries(table)) {
        expect(
          RIGHT_ARROW_RE.test(value),
          `forbidden right-arrow symbol in ${loc}["${key}"]: ${value}`,
        ).toBe(false);
      }
    }

    for (const [src, row] of Object.entries(EXACT_MESSAGES)) {
      expect(
        RIGHT_ARROW_RE.test(src),
        `forbidden right-arrow symbol in EXACT_MESSAGES key: ${src}`,
      ).toBe(false);
      for (const [loc, value] of Object.entries(row)) {
        expect(
          RIGHT_ARROW_RE.test(value),
          `forbidden right-arrow symbol in EXACT_MESSAGES["${src}"][${loc}]: ${value}`,
        ).toBe(false);
      }
    }
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
      "启动 llama.cpp：GPU + 内存标准 KV 兼容装配（--fit on · KV f16）",
      "启动 llama.cpp：GPU + 内存保守上下文装配（--fit on · 4K · KV q4_0）",
      "启动 llama.cpp：GPU + 内存 MoE 手动卸载回退（--fit off · 19 层专家内存卸载）",
      "模型权重已就绪（全量 GPU 驻留（FlashAttn · --fit on · KV q8_0），耗时 12.4 秒）",
      "方案「GPU + 内存智能混合装配（--fit on · 紧凑 KV q4_0）」未启动成功，自动尝试下一降级装载方案…",
      "方案「GPU + 内存标准 KV 兼容装配（--fit on · KV f16）」未启动成功（cudaMalloc failed: out of memory），自动尝试下一降级装载方案…",
      "当前显存紧张，上下文已自动调整为 4096 tokens 以完成装载",
      "模型加载失败（推理进程已退出，退出码 -9）：推理进程被系统终止（可能因系统内存不足 OOM）",
      "模型预热失败：cudaMalloc failed: out of memory",
      "CUDA 推理内核预热超时，推理服务将继续就绪",
      "依赖清单未变化，跳过重复安装，正在平滑停止旧管理服务",
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

  it("translates every backend exact catalog entry, subregion, startup stage, compound log, and ComfyUI field cleanly", () => {
    const leakRe = /[\u3400-\u9fff\uf900-\ufaff（）「」，。：；“”《》【】、]/;

    // 1. Every entry in EXACT_MESSAGES, SOURCE_LABELS, REDACTION_TOKENS, and SUBREGION_TRANSLATIONS
    for (const locale of ["en-US", "ko-KR"] as const) {
      for (const [key, row] of Object.entries(EXACT_MESSAGES)) {
        expect(
          leakRe.test(row[locale]),
          `EXACT_MESSAGES leak in ${locale}[${key}]: ${row[locale]}`,
        ).toBe(false);
      }
      for (const [key, row] of Object.entries(SOURCE_LABELS)) {
        expect(
          leakRe.test(row[locale]),
          `SOURCE_LABELS leak in ${locale}[${key}]: ${row[locale]}`,
        ).toBe(false);
      }
      for (const [key, row] of Object.entries(REDACTION_TOKENS)) {
        expect(
          leakRe.test(row[locale]),
          `REDACTION_TOKENS leak in ${locale}[${key}]: ${row[locale]}`,
        ).toBe(false);
      }
      for (const [sub, row] of Object.entries(SUBREGION_TRANSLATIONS)) {
        expect(row[locale], `Missing subregion ${sub} in ${locale}`).toBeTruthy();
        expect(
          leakRe.test(row[locale]),
          `SUBREGION_TRANSLATIONS leak in ${locale}[${sub}]: ${row[locale]}`,
        ).toBe(false);
      }
      for (const [code, label] of Object.entries(
        COUNTRY_OVERRIDES_BY_LOCALE[locale],
      )) {
        expect(
          leakRe.test(label),
          `COUNTRY_OVERRIDES leak in ${locale}[${code}]: ${label}`,
        ).toBe(false);
      }
    }

    // 2. Comprehensive dynamic/recursive backend log matrix across all 6 locales
    const dynamicSamples = [
      // Startup & bootstrap stages (including the exact screenshot banner)
      "启动远端 GPU",
      "新增启动远端 GPU",
      "正在分配 T4",
      "正在分配 G4（第 2/6 次）",
      "Google 算力紧张，正在排队分配 G4（第 2/10 次）",
      "Google 分配服务繁忙，正在重试分配 G4（第 2/10 次）",
      "正在排队分配 G4（算力池较紧张 · 第 3/10 次）",
      "Google Colab 当前 G4 算力紧张，暂未分配出 GPU（耗时 30.9 秒 · HTTP 503），6 秒后自动重试（第 2/10 次）",
      "Google Colab 当前 G4 分配服务繁忙，暂未分配出 GPU（耗时 30.8 秒 · HTTP 504），6 秒后自动重试（第 2/10 次）",
      "Google Colab 分配 G4 GPU 暂时繁忙或超时（HTTP 503），3 秒后自动重试（第 2/6 次）",
      "Google Colab 分配 G4 GPU 暂时繁忙或超时（请求超时），5 秒后自动重试（第 3/6 次）",
      "Google Colab 分配 G4 GPU 暂时繁忙或超时（耗时 30.8 秒 · HTTP 503），6 秒后自动重试（第 2/10 次）",
      "Google Colab 分配 G4 GPU 暂时繁忙或超时（耗时 12.5 秒 · 请求超时），12 秒后自动重试（第 3/10 次）",
      "Google Colab 分配 G4 GPU 暂时繁忙或超时（耗时 0.4 秒 · 网络异常），20 秒后自动重试（第 4/10 次）",
      "Google Colab 侧 G4 算力持续紧张，暂未放出空闲 GPU，客户端会继续自动排队；也可稍后改选 L4 / A100 / H100",
      "当前 Google Colab 的 G4 算力池较紧张，正在排队调度；可继续等待，或稍后改选 L4 / A100 / H100",
      "已通过状态核验确认 G4 GPU 实例分配成功",
      "Google Colab 当前 G4 算力紧张，暂未分配出 GPU（HTTP 503），已自动重试 9 次仍未排到可用资源，请稍后重试或切换其他 GPU 型号",
      "Google Colab 当前 G4 算力紧张，暂未分配出 GPU（HTTP 503），请稍后重试或切换其他 GPU 型号",
      "Google Colab 当前 G4 分配服务繁忙或超时，暂未分配出 GPU（HTTP 504），已自动重试 9 次仍未成功，请稍后重试或切换其他 GPU 型号",
      "Google Colab 当前 G4 分配服务繁忙或超时，暂未分配出 GPU（HTTP 504），请稍后重试或切换其他 GPU 型号",
      "Google Colab 分配 G4 GPU 暂时不可用（HTTP 503，该型号可能算力紧张或后端分配超时），已自动重试 5 次仍未成功，请稍后重试或切换其他 GPU 型号",
      "Google Colab 分配 G4 GPU 暂时不可用（HTTP 503，该型号可能算力紧张或后端分配超时），请稍后重试或切换其他 GPU 型号",
      "Google Colab 分配 G4 GPU 请求超时，已自动重试 5 次仍未成功，请稍后重试或切换其他 GPU 型号",
      "Google Colab 分配 G4 GPU 请求超时，请稍后重试或切换其他 GPU 型号",
      "Google Colab 拒绝分配 G4 GPU（HTTP 412）：当前账号活跃实例已达上限或受配额限制，请先释放已有实例或切换其他 GPU 型号",
      "Google Colab 拒绝分配 G4 GPU（HTTP 400）：当前账号可能暂无该型号配额或权限，请切换其他 GPU 型号",
      "Google Colab 授权校验失败（HTTP 403），请在设置中重新登录后重试",
      "Google Colab 分配请求过于频繁（HTTP 429），请稍等片刻后重试",
      "Google Colab 分配 G4 GPU 失败（HTTP 500），请稍后重试",
      "Google Colab 分配 G4 GPU 失败，请检查网络连接后重试",
      "远端初始化异常：RuntimeError: wheel install failed",
      "远端初始化未完成：RuntimeError: wheel install failed",
      "等待远端内核",
      "部署远端管理服务",
      "安装远端服务依赖",
      "启动远端管理服务",
      "正在自动挂载 Google 云盘 (ColabOne_Drive)",
      "启动远端 GPU：完成",
      "更新远端管理服务：完成",
      "释放远端实例 gpu-t4-hm-euw4c0-2grmqqk0hzj5n：完成",
      // Embedded location names & allocation metadata
      "已分配实例 gpu-t4-hm-euw4c0-2grmqqk0hzj5n（荷兰 · 埃姆斯哈文 · 高内存 · 1.25 CU/小时）",
      "已分配实例 gpu-l4-use1-abc（美国 · 南卡罗来纳 · 1.96 CU/小时）",
      "已分配实例 gpu-t4-euw1-xyz（比利时 · 圣吉兰）",
      // Recursive remote task logs (with inner " · " segments)
      "模型下载：进行中 · 30% · 进度 30.0% · 4.20/14.00 GiB · 120.5 MiB/s · 16 个连接",
      "模型下载：进行中 · 50% · 校验 SHA-256 · Qwen3.gguf",
      "模型下载：失败 · 30% · 进度 30.0% · 4.20/14.00 GiB · 120.5 MiB/s · 16 个连接 · 下载失败：分块传输未完成",
      "模型加载：失败 · 加载到远端显存 · 模型加载失败：out of memory",
      "ComfyUI 工作流：进行中 · Flux Demo · 排队中（第 1 位）· 已用 12 秒",
      "ComfyUI 工作流：进行中 · Flux Demo · 远端 GPU 执行中 · 已用 28 秒",
      "ComfyUI 工作流：失败 · 提交工作流：Flux Demo · ComfyUI 执行失败：节点 7(KSampler): 未知执行错误",
      // Persistent connection error banner
      "连接检查失败：ConnectionError。客户端会自动重试，请检查网络连接。",
      // Settings summary & idle countdown
      "已保存设置（默认 GPU: T4 · 空闲释放: 30 分钟 · 下载并发: 16 线程 · 分配重试上限: 20 次 · 已更新 Hugging Face Token）",
      "GPU 分配最大尝试次数必须在 1 至 100 次之间",
      "空闲自动释放倒计时剩余 45 秒；继续操作可重置倒计时",
      "服务返回 HTTP 403，请检查授权和配置",
      // Download / engine / ComfyUI / tunnel / Public API / Drive / VM boot
      "准备下载 Qwen3-32B：4 个文件，共 19.50 GiB，16 线程，目标为远端 GPU 磁盘",
      "远端可用磁盘 180.0 GiB，本次预计新增 19.5 GiB",
      "分块下载：16 块，每块最多 64 MiB，并发上限 16；复用 128.0 MiB",
      "分块 2/16 重试 1/3（TimeoutError）",
      "全部文件已验证：19.50 GiB，用时 64.2 秒，平均传输 310.4 MiB/s；复用 128.0 MiB",
      "CUDA 推理内核预热完成（耗时 2.4 秒）",
      "远端管理服务健康检查通过（耗时 3.1 秒）",
      "正在等待远端管理服务就绪（已耗时 12 秒）",
      "仍在安装远端服务依赖（已耗时 18 秒）",
      "远端服务依赖已就绪（耗时 24.5 秒）",
      "正在等待公网域名路由生效并完成鉴权探测（已耗时 10 秒）",
      "复用远端已校验的 Cloudflared 2025.4.0",
      "API 请求 #12 完成 · ≈42.5 tokens/s · 输出 256 tokens · 首字 320ms · 耗时 6.12s",
      "API 请求 #12 完成 · 54.2 tokens/s · 输出 256 tokens · 自动压缩 210528 至 12480 tokens · 首字 320ms · 耗时 6.12s",
      "API 请求 #14 上下文超限（约 210528 / 65536 tokens），已自动压缩为约 12480 tokens",
      "API 请求上下文超限（约 210528 / 65536 tokens），已自动压缩为约 12480 tokens",
      "输入上下文过长（210528 tokens），超过当前模型上下文上限（65536 tokens）",
      "输入上下文超过当前模型上限（65536 tokens）",
      "输入上下文超过当前模型上限",
      "API 请求 #13 已取消 · 耗时 1.40s",
      "API 请求 #14 失败 · 耗时 0.85s · 无法连接远端推理引擎",
      "API 请求 #15 · chat · 模型 gemma4-31b",
      "ComfyUI 拒绝了工作流：节点 3(CLIPTextEncode): missing input；节点 7(KSampler): invalid seed",
      "工作流目录不能是符号链接",
      "ComfyUI 模型子目录 checkpoints不能是符号链接",
      "Google 云盘连接失败：授权或网络异常",
      "云盘自动挂载未就绪，实例仍可正常使用：授权或网络异常",
      "正在上传 ComfyUI 输入素材：文件",
      "不支持的输入文件格式：未知",
      // ComfyUI field labels (static + dynamic)
      "宽度",
      "高度",
      "帧数",
      "帧率 (FPS)",
      "批次大小",
      "采样步数",
      "CFG 引导系数",
      "重绘幅度",
      "随机种子",
      "CLIP Text Encode · 正向提示词",
      "CLIP Text Encode · 反向提示词",
      "Prompt · 文本输入",
      "LoadImage · 参考图片",
      "LoadAudio · 输入音频",
      // GPU unavailable & redaction tokens
      "GPU 信息不可用",
      "远端设备 GPU 信息不可用，显存 15.0 GiB",
      "移除快捷方式失败：PermissionError [链接已隐藏] Bearer [已隐藏] [凭据已隐藏]",
    ];

    for (const locale of ["en-US", "ko-KR"] as const) {
      setColabOneLocale(locale, { persist: false, syncNative: false });
      for (const sample of dynamicSamples) {
        const out = tb(sample);
        expect(
          leakRe.test(out),
          `CJK/fullwidth leak in ${locale} for "${sample}" -> "${out}"`,
        ).toBe(false);
      }
    }

    // Spot-check exact screenshot repro & location in en-US
    setColabOneLocale("en-US", { persist: false, syncNative: false });
    expect(tb("启动远端管理服务")).toBe("Starting remote control service");
    expect(tb("安装远端服务依赖")).toBe("Installing remote service dependencies");
    expect(tb("启动远端 GPU：完成")).toBe("Starting remote GPU: Completed");
    expect(
      tb("已分配实例 gpu-t4-hm-euw4c0-2grmqqk0hzj5n（荷兰 · 埃姆斯哈文 · 高内存 · 1.25 CU/小时）"),
    ).toBe(
      "Allocated instance gpu-t4-hm-euw4c0-2grmqqk0hzj5n (Netherlands · Eemshaven · High-RAM · 1.25 CU/hr)",
    );
    expect(
      locationFull({
        country: "NL",
        country_name: "荷兰",
        region: "埃姆斯哈文",
        zone: "europe-west4-c",
      }),
    ).toBe("Netherlands · Eemshaven");
    expect(COLABONE_STATUS_LABELS.connecting).toBe("Connecting");
    expect(COLABONE_STATUS_LABELS.running).toBe("Running");

    // English-source runtime errors localize into zh-CN too
    setColabOneLocale("zh-CN", { persist: false, syncNative: false });
    expect(tb("ColabOne service failed to start")).toBe("远端管理服务启动失败");
    expect(tb("ColabOne service readiness timeout")).toBe("远端管理服务就绪超时");
    expect(tb("Unauthorized")).toBe("未经授权");
    expect(tb("Invalid OAuth state")).toBe("OAuth 状态校验无效");
    expect(tb("ComfyUI upstream is unreachable")).toBe("无法连接到远端 ComfyUI 上游服务");

    // Verify modelCompatibility & modelLibrary localization fixes
    setColabOneLocale("en-US", { persist: false, syncNative: false });
    const gpuTargets = buildColabOneGpuTargetOptions({
      state: "ready",
      gpu: { name: "GPU 信息不可用", total_mib: 16384, used_mib: 0 },
    } as RuntimeStatus);
    expect(leakRe.test(gpuTargets[0].subtitle)).toBe(false);
    expect(leakRe.test(gpuTargets[0].shortLabel)).toBe(false);

    const plan = planColabOneVersionOptions(
      [
        {
          filename: "broken.gguf",
          size: 4 * 1024 ** 3,
          shards: 2,
          quantization: "Q4_K_M",
          supported: false,
          reason: "缺少完整分片或 SHA-256 元数据",
        },
      ],
      gpuTargets[0],
    );
    expect(leakRe.test(plan.all[0].disabledReason)).toBe(false);

    const sampleModels: ModelSpec[] = [
      {
        id: "gemma4-31b",
        name: "Gemma-4-31B",
        description: "31B 稠密 / Q8_0",
        common: true,
        downloaded: false,
        loaded: false,
        size: 32 * 1024 ** 3,
        min_vram_mib: 0,
        files: [{ repo: "org/gemma4", path: "gemma4.gguf" }],
      },
    ];
    expect(
      filterCommonModels(sampleModels, "dense", { state: "stopped" }).length,
    ).toBe(1);
  });

  it("translates multi-line backend errors, numeric ComfyUI fields, and Wave 1/2 messages without CJK leaks", () => {
    const leakRe = /[\u3400-\u9fff\uf900-\ufaff（）「」，。：；“”《》【】、]/;
    const samples = [
      "模型加载失败：first line\nsecond line",
      "ComfyUI 执行失败：节点 3(KSampler): CUDA OOM\nstack trace line",
      "远端初始化未完成：RuntimeError: pip failed\nexit code 1",
      "模型预热失败（推理进程已退出，退出码 137）：CUDA OOM\nkilled",
      "Google 算力紧张，正在排队分配 G4（第 2/10 次）",
      "Google 分配服务繁忙，正在重试分配 G4（第 2/10 次）",
      "Google Colab 当前 G4 算力紧张，暂未分配出 GPU（耗时 30.9 秒 · HTTP 503），6 秒后自动重试（第 2/10 次）",
      "Google Colab 当前 G4 分配服务繁忙，暂未分配出 GPU（耗时 30.8 秒 · HTTP 504），6 秒后自动重试（第 2/10 次）",
      "Google Colab 侧 G4 算力持续紧张，暂未放出空闲 GPU，客户端会继续自动排队；也可稍后改选 L4 / A100 / H100",
      "Google Colab 当前 G4 算力紧张，暂未分配出 GPU（HTTP 503），已自动重试 9 次仍未排到可用资源，请稍后重试或切换其他 GPU 型号",
      "Google Colab 当前 G4 分配服务繁忙或超时，暂未分配出 GPU（HTTP 504），已自动重试 9 次仍未成功，请稍后重试或切换其他 GPU 型号",
      "Google Colab 分配 G4 GPU 暂时繁忙或超时（网络异常），16 秒后自动重试（第 5/10 次）",
      "Google Colab 分配 G4 GPU 网络连接中断，已自动重试 9 次仍未成功，请检查网络或代理后重试",
      "已取消 GPU 分配",
      "已取消 GPU 启动",
      "已请求取消 GPU 分配",
      "正在取消 GPU 分配",
      "正在取消启动并释放实例",
      "已取消启动，正在释放刚分配的实例",
      "取消启动时未能确认释放刚分配的实例。实例记录已保留，请在「远端设备」页立即重试停止实例",
      "启动已取消，正在释放刚分配的实例：gpu-g4-test",
      "请等待当前操作完成后再切换实例",
      "云盘历史数据恢复未完成，为避免覆盖请重试或勾选强制释放",
      "EmptyLatentImage · 宽度",
      "EmptyLatentImage · 高度",
      "EmptyHunyuanLatentVideo · 帧数",
      "CreateVideo · 帧率 (FPS)",
      "EmptyLatentImage · 批次大小",
      "KSampler · 采样步数",
      "KSampler · CFG 引导系数",
      "KSampler · 重绘幅度",
      "KSampler · 随机种子",
    ];
    for (const loc of ["en-US", "ko-KR"] as const) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const sample of samples) {
        const out = tb(sample);
        expect(out, `${loc}: ${sample}`).not.toBe(sample);
        expect(leakRe.test(out), `${loc} leak in "${out}"`).toBe(false);
      }
    }
    setColabOneLocale("zh-TW", { persist: false, syncNative: false });
    expect(tb("请等待当前操作完成后再切换实例")).toContain("執行個體");
    setColabOneLocale("zh-HK", { persist: false, syncNative: false });
    expect(tb("请等待当前操作完成后再切换实例")).toContain("實例");
  });

  it("guards against mistranslated quantization, DriveFS, regional, and removed-budget copy", () => {
    // 1. No image-quality wording ("高画质"/"高精細") for LLM quantization
    for (const loc of ALL_LOCALES) {
      const q8Title = COLABONE_MESSAGES[loc]["models.compatQuantQ8Title"];
      expect(q8Title, loc).not.toContain("高画质");
      expect(q8Title, loc).not.toContain("高精細");
    }

    // 2. No ambiguous "原生驱动"/"原生驅動"/"ネイティブ駆動" in UI messages or backend exact catalog
    for (const loc of ALL_LOCALES) {
      for (const [key, val] of Object.entries(COLABONE_MESSAGES[loc])) {
        expect(val, `${loc}[${key}]`).not.toMatch(/原生驱[动動]|原生驅動|ネイティブ駆動/);
      }
    }
    for (const [src, row] of Object.entries(EXACT_MESSAGES)) {
      for (const loc of ALL_LOCALES) {
        expect(row[loc], `EXACT[${src}][${loc}]`).not.toMatch(
          /原生驱[动動]|原生驅動|ネイティブ駆動|预算保护|預算保護|budget-guard|Budget guard/,
        );
      }
    }

    // 3. No Japanese katakana middot (・) in non-ja-JP locales, and no Mainland "顯存" in zh-TW
    for (const loc of ALL_LOCALES) {
      if (loc === "ja-JP") continue;
      for (const [key, val] of Object.entries(COLABONE_MESSAGES[loc])) {
        expect(val, `${loc}[${key}] katakana middot`).not.toContain("\u30fb");
      }
    }
    for (const [key, val] of Object.entries(COLABONE_MESSAGES["zh-TW"])) {
      expect(val, `zh-TW[${key}] Mainland 顯存 leak`).not.toContain("顯存");
    }
    const spokenCantoneseRe = /[搵唔喺嘅啲咗冇]/;
    for (const [key, val] of Object.entries(COLABONE_MESSAGES["zh-HK"])) {
      expect(val, `zh-HK[${key}] spoken Cantonese`).not.toMatch(spokenCantoneseRe);
    }
    for (const [src, row] of Object.entries(EXACT_MESSAGES)) {
      expect(row["zh-HK"], `EXACT[${src}][zh-HK] spoken Cantonese`).not.toMatch(
        spokenCantoneseRe,
      );
    }

    // 4. Updated backend messages translate cleanly across en-US and ko-KR
    const updatedSamples = [
      "登录未完成，请返回 ColabOne 重试。",
      "等待登录超时。可点击「改用手动授权登录」，粘贴浏览器返回内容自动完成。",
      "启动失败且释放未确认。实例记录已保留，请在「远端设备」页立即重试停止实例",
      "对外 API 已关闭，远端模型仍保留在显存中",
      "请求参数不受支持，请查看 ColabOne「对外 API」中的接口列表与限制",
      "等待超时或调用已断开；若为生成接口，可减小输出长度上限或开启流式调用（stream=true）",
      "正在重新装载 Qwen3-32B，先卸载当前已装载模型以释放显存",
      "ComfyUI 模型子目录 checkpoints 不能是符号链接",
      "已启动远端自动释放保护守护进程",
      "远端自动释放保护即将释放实例，新服务会在下次启动时生效",
      "远端自动释放保护即将释放实例，未替换运行中的服务",
      "新服务未就绪，已恢复旧服务；模型文件与运行配置保持不变",
      "新服务未就绪，已恢复旧服务；模型文件与运行配置保持不变（日志：stderr tail）",
      "该模型属于图片/视频生成类（ComfyUI 专用），无法在「对外 API」文本引擎中装载，请前往 ComfyUI 工作区使用",
      "正在通过 Colab 原生 DriveFS 挂载 Google 云盘 (ColabOne_Drive)",
      "授权已确认，正在远端挂载 Google 云盘 (ColabOne_Drive)",
      "模型 API ID 必须为字符串",
      "模型 API ID 不能为空",
      "colabone/auto 为系统保留别名，请使用其他模型 ID",
      "模型 API ID 仅支持字母、数字、点、下划线、连字符和斜杠，且不能以斜杠或点结尾",
      "找不到指定模型",
      "该模型 ID 已被其他模型使用，请换一个名称",
      "请先更新远端服务后再自定义模型 ID",
      "已更新模型 API ID：colabone/qwen3.8-flash-next",
      "已保存设置（默认 GPU: T4 · 空闲释放: 30 分钟 · 下载并发: 16 线程 · 已启用仅分配代理（SOCKS5 127.0.0.1:1080））",
      "已保存设置（默认 GPU: T4 · 空闲释放: 30 分钟 · 下载并发: 16 线程 · 已清除分配代理）",
      "本次 GPU 分配请求将经由代理（SOCKS5 127.0.0.1:1080），后续连接与推理均不走代理",
      "分配代理连通性测试通过（HTTP 127.0.0.1:7890 · 142 ms）",
      "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理",
      "GPU 分配代理认证失败，请检查代理用户名与密码后重试",
      "仅 GPU 分配请求允许使用分配代理",
      "更新前正在关闭对外 API 服务",
      "更新前正在取消运行中的远端任务",
      "更新前正在停止 ComfyUI 服务",
      "更新前正在卸载当前显存模型（保留磁盘缓存）",
      "文件大小必须大于 0",
      "工作流名称长度不合法",
      "工作流描述长度不能超过 1000",
      "字段 3.steps 的覆盖值类型不合法",
      "字段 3.clip skip 的覆盖值类型不合法",
      "字段 3.clip skip 包含非法浮点值",
      "字段 3.clip skip 不能为空值",
      "字段 mode 不能为空值",
      "字段 options 不能为空值",
      "ComfyUI 已就绪，正在按原配置自动恢复原生画布隧道",
      "等待 ComfyUI 启动超时，未能自动恢复原生画布，请手动开启",
      "正在按更新前规格自动重新装载模型 Qwen3.8（8K · 温度 0.7）",
      "正在按更新前规格自动重新装载模型 Qwen3.8（4096 tokens · 温度 0.7）",
      "正在按更新前规格自动重新装载模型 Qwen3.8（上下文 8192 · 温度 0.7）",
      "正在按更新前规格自动重新装载模型 Qwen3.8（温度 0.7 · 8K）",
      "正在按更新前规格自动重新装载模型 Qwen3.8",
      "自动重新装载模型 Qwen3.8 失败，请手动装载：远端服务未返回响应",
      "正在按更新前配置自动重新开启对外 API 服务与公网隧道",
      "自动恢复对外 API 失败，请手动开启：远端服务未返回响应",
      "正在自动重新启动 ComfyUI 工作流引擎",
      "自动重新启动 ComfyUI 失败，请手动启动：远端服务未返回响应",
      "自动恢复 ComfyUI 原生画布失败，请手动开启：远端服务未返回响应",
      "已预约 ComfyUI 就绪后自动按原配置恢复原生画布隧道",
      "Google Colab 返回了空的实例分配结果",
      "客户端正在关闭",
      "远端快照格式无效",
      "远端实例未连接",
      "远端实例硬件监控通道已关闭",
      "远端实例管理通道已关闭",
      "读取本地模型库配置失败，已保留原文件未覆盖",
      "未知字段: extra_field",
      "模型文件配置包含不支持的字段: extra_field",
      "模型文件配置包含不支持的字段: extra field",
      "模型文件字段 sha256 不能为空值",
      "模型配置包含不支持的字段: unknown_key",
      "模型配置字段 files 不能为空值",
      "模型文件大小必须大于 0",
      "模型名称长度需在 1 到 200 之间",
      "ComfyUI 端口必须在 1024 到 65535 之间",
    ];
    const leakRe = /[\u3400-\u9fff\uf900-\ufaff（）「」，。：；“”《》【】、]/;
    for (const loc of ["en-US", "ko-KR"] as const) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const sample of updatedSamples) {
        const out = tb(sample);
        expect(out, `${loc}: ${sample}`).not.toBe(sample);
        expect(leakRe.test(out), `${loc} leak in "${out}"`).toBe(false);
      }
    }

    // Verify all 4 Go models.go validation errors across all 6 locales
    const modelsGoErrors = [
      "模型文件配置包含不支持的字段: extra_field",
      "模型文件字段 sha256 不能为空值",
      "模型配置包含不支持的字段: unknown_key",
      "模型配置字段 files 不能为空值",
    ];
    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const msg of modelsGoErrors) {
        const out = tb(msg);
        if (loc === "zh-CN") {
          expect(out).toBe(msg);
        } else {
          expect(out, `${loc}: ${msg}`).not.toBe(msg);
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(leakRe.test(out), `${loc} leak in "${out}"`).toBe(false);
        }
        if (loc === "zh-HK") {
          expect(out, `zh-HK spoken Cantonese in "${out}"`).not.toMatch(
            spokenCantoneseRe,
          );
        }
      }
    }
    setColabOneLocale("zh-TW", { persist: false, syncNative: false });
    expect(tb("模型文件配置包含不支持的字段: extra_field")).toBe(
      "模型檔案設定包含不支援的欄位：extra_field",
    );
    expect(tb("模型文件字段 sha256 不能为空值")).toBe(
      "模型檔案欄位 sha256 不可為空值",
    );
    expect(tb("模型配置包含不支持的字段: unknown_key")).toBe(
      "模型設定包含不支援的欄位：unknown_key",
    );
    expect(tb("模型配置字段 files 不能为空值")).toBe(
      "模型設定欄位 files 不可為空值",
    );
    setColabOneLocale("zh-HK", { persist: false, syncNative: false });
    expect(tb("模型文件字段 sha256 不能为空值")).toBe(
      "模型檔案欄位 sha256 不能為空值",
    );
    expect(tb("模型配置字段 files 不能为空值")).toBe(
      "模型設定欄位 files 不能為空值",
    );

    setColabOneLocale("en-US", { persist: false, syncNative: false });
    expect(tb("模型文件配置包含不支持的字段: extra_field")).toBe(
      "Model file specification contains unsupported field: extra_field",
    );
    expect(tb("模型文件字段 sha256 不能为空值")).toBe(
      "Model file field sha256 cannot be null",
    );
    expect(tb("模型配置包含不支持的字段: unknown_key")).toBe(
      "Model specification contains unsupported field: unknown_key",
    );
    expect(tb("模型配置字段 files 不能为空值")).toBe(
      "Model specification field files cannot be null",
    );
    expect(tb("字段 3.clip skip 的覆盖值类型不合法")).toBe(
      "Invalid override value type for field 3.clip skip",
    );
    expect(tb("字段 3.clip skip 包含非法浮点值")).toBe(
      "Field 3.clip skip contains an invalid floating-point value",
    );
    expect(tb("字段 3.clip skip 不能为空值")).toBe(
      "Field 3.clip skip cannot be null",
    );
    expect(tb("字段 options 不能为空值")).toBe("Field options cannot be null");
    expect(tb("正在按更新前规格自动重新装载模型 Qwen3（微调）")).toBe(
      "Automatically reloading model Qwen3（微调） with previous settings",
    );
    expect(
      tb("正在按更新前规格自动重新装载模型 Qwen3（微调）（8K · 温度 0.7）"),
    ).toBe(
      "Automatically reloading model Qwen3（微调） with previous settings (8K · temperature 0.7)",
    );
    expect(
      tb("正在按更新前规格自动重新装载模型 Qwen3（温度 0.7）（微调）"),
    ).toBe(
      "Automatically reloading model Qwen3（温度 0.7）（微调） with previous settings",
    );
    expect(
      tb(
        "正在按更新前规格自动重新装载模型 Qwen3（温度 0.7）（微调）（8K · 温度 0.7）",
      ),
    ).toBe(
      "Automatically reloading model Qwen3（温度 0.7）（微调） with previous settings (8K · temperature 0.7)",
    );
  });

  it("preserves ComfyUI subgraph node IDs containing colons across all 6 locales", () => {
    const leakRe = /[\u3400-\u9fff\uf900-\ufaff（）「」，。：；“”《》【】、]/;
    const simplifiedLeakRe = /节点|必须为对象|输入名称|未知执行错误|执行失败/;

    const expectedByMessage: Record<string, Record<ColabOneLocale, string>> = {
      "节点 3_a:1 必须为对象": {
        "zh-CN": "节点 3_a:1 必须为对象",
        "en-US": "Node 3_a:1 must be an object",
        "zh-TW": "節點 3_a:1 必須為物件",
        "zh-HK": "節點 3_a:1 必須為物件",
        "ja-JP": "ノード 3_a:1 はオブジェクトである必要があります",
        "ko-KR": "노드 3_a:1은(는) 객체여야 합니다",
      },
      "节点 3_a:1 缺少有效的 class_type": {
        "zh-CN": "节点 3_a:1 缺少有效的 class_type",
        "en-US": "Node 3_a:1 is missing a valid class_type",
        "zh-TW": "節點 3_a:1 缺少有效的 class_type",
        "zh-HK": "節點 3_a:1 缺少有效的 class_type",
        "ja-JP": "ノード 3_a:1 に有効な class_type がありません",
        "ko-KR": "노드 3_a:1에 유효한 class_type이 없습니다",
      },
      "节点 3_a:1 缺少有效的 inputs 对象": {
        "zh-CN": "节点 3_a:1 缺少有效的 inputs 对象",
        "en-US": "Node 3_a:1 is missing a valid inputs object",
        "zh-TW": "節點 3_a:1 缺少有效的 inputs 物件",
        "zh-HK": "節點 3_a:1 缺少有效的 inputs 物件",
        "ja-JP": "ノード 3_a:1 に有効な inputs オブジェクトがありません",
        "ko-KR": "노드 3_a:1에 유효한 inputs 객체가 없습니다",
      },
      "节点 3_a:1 包含非法输入名称": {
        "zh-CN": "节点 3_a:1 包含非法输入名称",
        "en-US": "Node 3_a:1 contains an invalid input name",
        "zh-TW": "節點 3_a:1 包含無效的輸入名稱",
        "zh-HK": "節點 3_a:1 包含無效的輸入名稱",
        "ja-JP": "ノード 3_a:1 に無効な入力名が含まれています",
        "ko-KR": "노드 3_a:1에 유효하지 않은 입력 이름이 포함되어 있습니다",
      },
      "节点 10:3(KSampler): Out of memory": {
        "zh-CN": "节点 10:3(KSampler): Out of memory",
        "en-US": "Node 10:3 (KSampler): Out of memory",
        "zh-TW": "節點 10:3(KSampler): Out of memory",
        "zh-HK": "節點 10:3(KSampler): Out of memory",
        "ja-JP": "ノード 10:3(KSampler): Out of memory",
        "ko-KR": "노드 10:3 (KSampler): Out of memory",
      },
      "节点 7(KSampler): 未知执行错误": {
        "zh-CN": "节点 7(KSampler): 未知执行错误",
        "en-US": "Node 7 (KSampler): Unknown execution error",
        "zh-TW": "節點 7(KSampler): 未知執行錯誤",
        "zh-HK": "節點 7(KSampler): 未知執行錯誤",
        "ja-JP": "ノード 7(KSampler): 不明な実行エラー",
        "ko-KR": "노드 7 (KSampler): 알 수 없는 실행 오류",
      },
      "节点 10:3: missing input": {
        "zh-CN": "节点 10:3: missing input",
        "en-US": "Node 10:3: missing input",
        "zh-TW": "節點 10:3: missing input",
        "zh-HK": "節點 10:3: missing input",
        "ja-JP": "ノード 10:3: missing input",
        "ko-KR": "노드 10:3: missing input",
      },
      "节点 10:3(KSampler (Efficient)): Out of memory": {
        "zh-CN": "节点 10:3(KSampler (Efficient)): Out of memory",
        "en-US": "Node 10:3 (KSampler (Efficient)): Out of memory",
        "zh-TW": "節點 10:3(KSampler (Efficient)): Out of memory",
        "zh-HK": "節點 10:3(KSampler (Efficient)): Out of memory",
        "ja-JP": "ノード 10:3(KSampler (Efficient)): Out of memory",
        "ko-KR": "노드 10:3 (KSampler (Efficient)): Out of memory",
      },
      "节点 10:3:5(Foo (v2)): bad value": {
        "zh-CN": "节点 10:3:5(Foo (v2)): bad value",
        "en-US": "Node 10:3:5 (Foo (v2)): bad value",
        "zh-TW": "節點 10:3:5(Foo (v2)): bad value",
        "zh-HK": "節點 10:3:5(Foo (v2)): bad value",
        "ja-JP": "ノード 10:3:5(Foo (v2)): bad value",
        "ko-KR": "노드 10:3:5 (Foo (v2)): bad value",
      },
      "节点 10:3(KSampler (Efficient)): failed (cuda): retry": {
        "zh-CN": "节点 10:3(KSampler (Efficient)): failed (cuda): retry",
        "en-US": "Node 10:3 (KSampler (Efficient)): failed (cuda): retry",
        "zh-TW": "節點 10:3(KSampler (Efficient)): failed (cuda): retry",
        "zh-HK": "節點 10:3(KSampler (Efficient)): failed (cuda): retry",
        "ja-JP": "ノード 10:3(KSampler (Efficient)): failed (cuda): retry",
        "ko-KR": "노드 10:3 (KSampler (Efficient)): failed (cuda): retry",
      },
      "节点 10: detail (extra): more": {
        "zh-CN": "节点 10: detail (extra): more",
        "en-US": "Node 10: detail (extra): more",
        "zh-TW": "節點 10: detail (extra): more",
        "zh-HK": "節點 10: detail (extra): more",
        "ja-JP": "ノード 10: detail (extra): more",
        "ko-KR": "노드 10: detail (extra): more",
      },
      "ComfyUI 执行失败：节点 10:3(Load Image (Upload)): 未知执行错误": {
        "zh-CN": "ComfyUI 执行失败：节点 10:3(Load Image (Upload)): 未知执行错误",
        "en-US": "ComfyUI execution failed: Node 10:3 (Load Image (Upload)): Unknown execution error",
        "zh-TW": "ComfyUI 執行失敗：節點 10:3(Load Image (Upload)): 未知執行錯誤",
        "zh-HK": "ComfyUI 執行失敗：節點 10:3(Load Image (Upload)): 未知執行錯誤",
        "ja-JP": "ComfyUI の実行に失敗しました: ノード 10:3(Load Image (Upload)): 不明な実行エラー",
        "ko-KR": "ComfyUI 실행 실패: 노드 10:3 (Load Image (Upload)): 알 수 없는 실행 오류",
      },
    };

    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const [sample, expected] of Object.entries(expectedByMessage)) {
        const out = tb(sample);
        expect(out, `${loc}: ${sample}`).toBe(expected[loc]);
        if (loc !== "zh-CN") {
          expect(
            simplifiedLeakRe.test(out),
            `Simplified Chinese leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            leakRe.test(out),
            `CJK/fullwidth leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
        }
      }
    }

    // Wrapped ComfyUI error messages via translateComfyErrorDetail preserve subgraph IDs too
    setColabOneLocale("en-US", { persist: false, syncNative: false });
    expect(tb("工作流节点校验失败：节点 3_a:1 必须为对象")).toBe(
      "Workflow node validation failed: Node 3_a:1 must be an object",
    );
    expect(tb("ComfyUI 执行失败：节点 10:3(KSampler): Out of memory")).toBe(
      "ComfyUI execution failed: Node 10:3 (KSampler): Out of memory",
    );
    expect(tb("ComfyUI 拒绝了工作流：节点 10:3: missing input")).toBe(
      "ComfyUI rejected workflow: Node 10:3: missing input",
    );
  });

  it("handles trailing-colon node IDs, multi-line ComfyUI wrapper errors, and inner ): inside class_type across all 6 locales", () => {
    const leakRe = /[\u3400-\u9fff\uf900-\ufaff（）「」，。：；“”《》【】、]/;
    const simplifiedLeakRe =
      /节点|必须|对象|输入|执行|错误|拒绝|校验|任务|编号|失败/;

    const expectedByMessage: Record<string, Record<ColabOneLocale, string>> = {
      // 1. Trailing-colon node IDs in schema-validation errors (direct and wrapped)
      "节点 3_a: 必须为对象": {
        "zh-CN": "节点 3_a: 必须为对象",
        "en-US": "Node 3_a: must be an object",
        "zh-TW": "節點 3_a: 必須為物件",
        "zh-HK": "節點 3_a: 必須為物件",
        "ja-JP": "ノード 3_a: はオブジェクトである必要があります",
        "ko-KR": "노드 3_a:은(는) 객체여야 합니다",
      },
      "节点 3_a: 缺少有效的 class_type": {
        "zh-CN": "节点 3_a: 缺少有效的 class_type",
        "en-US": "Node 3_a: is missing a valid class_type",
        "zh-TW": "節點 3_a: 缺少有效的 class_type",
        "zh-HK": "節點 3_a: 缺少有效的 class_type",
        "ja-JP": "ノード 3_a: に有効な class_type がありません",
        "ko-KR": "노드 3_a:에 유효한 class_type이 없습니다",
      },
      "节点 3_a: 缺少有效的 inputs 对象": {
        "zh-CN": "节点 3_a: 缺少有效的 inputs 对象",
        "en-US": "Node 3_a: is missing a valid inputs object",
        "zh-TW": "節點 3_a: 缺少有效的 inputs 物件",
        "zh-HK": "節點 3_a: 缺少有效的 inputs 物件",
        "ja-JP": "ノード 3_a: に有効な inputs オブジェクトがありません",
        "ko-KR": "노드 3_a:에 유효한 inputs 객체가 없습니다",
      },
      "节点 3_a: 包含非法输入名称": {
        "zh-CN": "节点 3_a: 包含非法输入名称",
        "en-US": "Node 3_a: contains an invalid input name",
        "zh-TW": "節點 3_a: 包含無效的輸入名稱",
        "zh-HK": "節點 3_a: 包含無效的輸入名稱",
        "ja-JP": "ノード 3_a: に無効な入力名が含まれています",
        "ko-KR": "노드 3_a:에 유효하지 않은 입력 이름이 포함되어 있습니다",
      },
      "工作流节点校验失败：节点 3_a: 必须为对象": {
        "zh-CN": "工作流节点校验失败：节点 3_a: 必须为对象",
        "en-US": "Workflow node validation failed: Node 3_a: must be an object",
        "zh-TW": "工作流程節點驗證失敗：節點 3_a: 必須為物件",
        "zh-HK": "工作流節點校驗失敗：節點 3_a: 必須為物件",
        "ja-JP":
          "ワークフローノードの検証に失敗しました: ノード 3_a: はオブジェクトである必要があります",
        "ko-KR": "워크플로 노드 검증 실패: 노드 3_a:은(는) 객체여야 합니다",
      },
      "节点 3_a:: missing input": {
        "zh-CN": "节点 3_a:: missing input",
        "en-US": "Node 3_a:: missing input",
        "zh-TW": "節點 3_a:: missing input",
        "zh-HK": "節點 3_a:: missing input",
        "ja-JP": "ノード 3_a:: missing input",
        "ko-KR": "노드 3_a:: missing input",
      },

      // 2. Multi-line ComfyUI rejection / validation / prompt-ID & compound task errors
      "ComfyUI 拒绝了工作流：节点 10(KSampler): invalid input\nline 2": {
        "zh-CN": "ComfyUI 拒绝了工作流：节点 10(KSampler): invalid input\nline 2",
        "en-US":
          "ComfyUI rejected workflow: Node 10 (KSampler): invalid input\nline 2",
        "zh-TW":
          "ComfyUI 拒絕了工作流程：節點 10(KSampler): invalid input\nline 2",
        "zh-HK":
          "ComfyUI 拒絕了工作流：節點 10(KSampler): invalid input\nline 2",
        "ja-JP":
          "ComfyUI がワークフローを拒否しました: ノード 10(KSampler): invalid input\nline 2",
        "ko-KR":
          "ComfyUI가 워크플로를 거부했습니다: 노드 10 (KSampler): invalid input\nline 2",
      },
      "工作流节点校验失败：节点 10(KSampler): invalid input\nline 2": {
        "zh-CN": "工作流节点校验失败：节点 10(KSampler): invalid input\nline 2",
        "en-US":
          "Workflow node validation failed: Node 10 (KSampler): invalid input\nline 2",
        "zh-TW":
          "工作流程節點驗證失敗：節點 10(KSampler): invalid input\nline 2",
        "zh-HK":
          "工作流節點校驗失敗：節點 10(KSampler): invalid input\nline 2",
        "ja-JP":
          "ワークフローノードの検証に失敗しました: ノード 10(KSampler): invalid input\nline 2",
        "ko-KR":
          "워크플로 노드 검증 실패: 노드 10 (KSampler): invalid input\nline 2",
      },
      "ComfyUI 未返回有效任务编号：节点 10(KSampler): invalid input\nline 2": {
        "zh-CN":
          "ComfyUI 未返回有效任务编号：节点 10(KSampler): invalid input\nline 2",
        "en-US":
          "ComfyUI did not return a valid prompt ID: Node 10 (KSampler): invalid input\nline 2",
        "zh-TW":
          "ComfyUI 未回傳有效任務編號：節點 10(KSampler): invalid input\nline 2",
        "zh-HK":
          "ComfyUI 未返回有效任務編號：節點 10(KSampler): invalid input\nline 2",
        "ja-JP":
          "ComfyUI から有効なジョブ ID が返されませんでした: ノード 10(KSampler): invalid input\nline 2",
        "ko-KR":
          "ComfyUI가 유효한 작업 ID를 반환하지 않았습니다: 노드 10 (KSampler): invalid input\nline 2",
      },
      "ComfyUI 工作流：失败 · 提交工作流：Flux Demo · ComfyUI 拒绝了工作流：节点 10(KSampler): invalid\nline 2":
        {
          "zh-CN":
            "ComfyUI 工作流：失败 · 提交工作流：Flux Demo · ComfyUI 拒绝了工作流：节点 10(KSampler): invalid\nline 2",
          "en-US":
            "ComfyUI workflow: Failed · Submitting workflow: Flux Demo · ComfyUI rejected workflow: Node 10 (KSampler): invalid\nline 2",
          "zh-TW":
            "ComfyUI 工作流程：失敗 · 提交工作流程：Flux Demo · ComfyUI 拒絕了工作流程：節點 10(KSampler): invalid\nline 2",
          "zh-HK":
            "ComfyUI 工作流：失敗 · 提交工作流：Flux Demo · ComfyUI 拒絕了工作流：節點 10(KSampler): invalid\nline 2",
          "ja-JP":
            "ComfyUI ワークフロー: 失敗 · ワークフローを送信中: Flux Demo · ComfyUI がワークフローを拒否しました: ノード 10(KSampler): invalid\nline 2",
          "ko-KR":
            "ComfyUI 워크플로: 실패 · 워크플로 제출 중: Flux Demo · ComfyUI가 워크플로를 거부했습니다: 노드 10 (KSampler): invalid\nline 2",
        },

      // 3. Inner ): inside parenthesized class_type & unmatched-( fallback
      "节点 10(Foo (Bar): Baz): 未知执行错误": {
        "zh-CN": "节点 10(Foo (Bar): Baz): 未知执行错误",
        "en-US": "Node 10 (Foo (Bar): Baz): Unknown execution error",
        "zh-TW": "節點 10(Foo (Bar): Baz): 未知執行錯誤",
        "zh-HK": "節點 10(Foo (Bar): Baz): 未知執行錯誤",
        "ja-JP": "ノード 10(Foo (Bar): Baz): 不明な実行エラー",
        "ko-KR": "노드 10 (Foo (Bar): Baz): 알 수 없는 실행 오류",
      },
      "节点 10:3(Foo (Bar): Baz): 未知执行错误": {
        "zh-CN": "节点 10:3(Foo (Bar): Baz): 未知执行错误",
        "en-US": "Node 10:3 (Foo (Bar): Baz): Unknown execution error",
        "zh-TW": "節點 10:3(Foo (Bar): Baz): 未知執行錯誤",
        "zh-HK": "節點 10:3(Foo (Bar): Baz): 未知執行錯誤",
        "ja-JP": "ノード 10:3(Foo (Bar): Baz): 不明な実行エラー",
        "ko-KR": "노드 10:3 (Foo (Bar): Baz): 알 수 없는 실행 오류",
      },
      "节点 10(Foo (Bar): 未知执行错误": {
        "zh-CN": "节点 10(Foo (Bar): 未知执行错误",
        "en-US": "Node 10 (Foo (Bar): Unknown execution error",
        "zh-TW": "節點 10(Foo (Bar): 未知執行錯誤",
        "zh-HK": "節點 10(Foo (Bar): 未知執行錯誤",
        "ja-JP": "ノード 10(Foo (Bar): 不明な実行エラー",
        "ko-KR": "노드 10 (Foo (Bar): 알 수 없는 실행 오류",
      },
      "节点 10(Foo (Bar): invalid\nline 2": {
        "zh-CN": "节点 10(Foo (Bar): invalid\nline 2",
        "en-US": "Node 10 (Foo (Bar): invalid\nline 2",
        "zh-TW": "節點 10(Foo (Bar): invalid\nline 2",
        "zh-HK": "節點 10(Foo (Bar): invalid\nline 2",
        "ja-JP": "ノード 10(Foo (Bar): invalid\nline 2",
        "ko-KR": "노드 10 (Foo (Bar): invalid\nline 2",
      },
    };

    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const [sample, expected] of Object.entries(expectedByMessage)) {
        const out = tb(sample);
        expect(out, `${loc}: ${sample}`).toBe(expected[loc]);
        if (loc !== "zh-CN") {
          expect(
            simplifiedLeakRe.test(out),
            `Simplified Chinese leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            leakRe.test(out),
            `CJK/fullwidth leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
        }
      }
    }
  });

  it("handles unmatched-) ComfyUI class_type fallback, preserves non-matching node strings, and translates multi-line auto-restore errors across all 6 locales", () => {
    const leakRe = /[\u3400-\u9fff\uf900-\ufaff（）「」，。：；“”《》【】、]/;
    const simplifiedLeakRe =
      /节点|未知执行错误|自动|装载|加载|失败|请手动|对外|开启|启动|画布/;

    const expectedByMessage: Record<string, Record<ColabOneLocale, string>> = {
      // 1. ComfyUI custom node class names containing unmatched ')'
      "节点 10(KSampler) v2): 未知执行错误": {
        "zh-CN": "节点 10(KSampler) v2): 未知执行错误",
        "en-US": "Node 10 (KSampler) v2): Unknown execution error",
        "zh-TW": "節點 10(KSampler) v2): 未知執行錯誤",
        "zh-HK": "節點 10(KSampler) v2): 未知執行錯誤",
        "ja-JP": "ノード 10(KSampler) v2): 不明な実行エラー",
        "ko-KR": "노드 10 (KSampler) v2): 알 수 없는 실행 오류",
      },
      "节点 10(Foo (v1))): 未知执行错误": {
        "zh-CN": "节点 10(Foo (v1))): 未知执行错误",
        "en-US": "Node 10 (Foo (v1))): Unknown execution error",
        "zh-TW": "節點 10(Foo (v1))): 未知執行錯誤",
        "zh-HK": "節點 10(Foo (v1))): 未知執行錯誤",
        "ja-JP": "ノード 10(Foo (v1))): 不明な実行エラー",
        "ko-KR": "노드 10 (Foo (v1))): 알 수 없는 실행 오류",
      },

      // 2. Multi-line auto-restore failure errors for all 4 rules
      "自动重新装载模型 Qwen3 失败，请手动装载：模型加载失败：first\nsecond": {
        "zh-CN":
          "自动重新装载模型 Qwen3 失败，请手动装载：模型加载失败：first\nsecond",
        "en-US":
          "Failed to reload model Qwen3 automatically; please load it manually: Model load failed: first\nsecond",
        "zh-TW":
          "自動重新載入模型 Qwen3 失敗，請手動載入：模型載入失敗：first\nsecond",
        "zh-HK":
          "自動重新載入模型 Qwen3 失敗，請手動載入：模型載入失敗：first\nsecond",
        "ja-JP":
          "モデル Qwen3 の自動再読み込みに失敗しました。手動で読み込んでください: モデルのロードに失敗しました: first\nsecond",
        "ko-KR":
          "모델 Qwen3 자동 다시 불러오기에 실패했습니다. 수동으로 불러와 주세요: 모델 로드 실패: first\nsecond",
      },
      "自动恢复对外 API 失败，请手动开启：first\nsecond": {
        "zh-CN": "自动恢复对外 API 失败，请手动开启：first\nsecond",
        "en-US":
          "Failed to restore Public API automatically; please enable it manually: first\nsecond",
        "zh-TW": "自動還原對外 API 失敗，請手動開啟：first\nsecond",
        "zh-HK": "自動恢復對外 API 失敗，請手動開啟：first\nsecond",
        "ja-JP":
          "パブリック API の自動復元に失敗しました。手動で起動してください: first\nsecond",
        "ko-KR":
          "외부 API 자동 복원에 실패했습니다. 수동으로 켜 주세요: first\nsecond",
      },
      "自动重新启动 ComfyUI 失败，请手动启动：first\nsecond": {
        "zh-CN": "自动重新启动 ComfyUI 失败，请手动启动：first\nsecond",
        "en-US":
          "Failed to restart ComfyUI automatically; please start it manually: first\nsecond",
        "zh-TW": "自動重新啟動 ComfyUI 失敗，請手動啟動：first\nsecond",
        "zh-HK": "自動重新啟動 ComfyUI 失敗，請手動啟動：first\nsecond",
        "ja-JP":
          "ComfyUI の自動再起動に失敗しました。手動で起動してください: first\nsecond",
        "ko-KR":
          "ComfyUI 자동 재시작에 실패했습니다. 수동으로 시작해 주세요: first\nsecond",
      },
      "自动恢复 ComfyUI 原生画布失败，请手动开启：first\nsecond": {
        "zh-CN": "自动恢复 ComfyUI 原生画布失败，请手动开启：first\nsecond",
        "en-US":
          "Failed to restore ComfyUI Native Canvas automatically; please enable it manually: first\nsecond",
        "zh-TW": "自動還原 ComfyUI 原生畫布失敗，請手動開啟：first\nsecond",
        "zh-HK": "自動恢復 ComfyUI 原生畫布失敗，請手動開啟：first\nsecond",
        "ja-JP":
          "ComfyUI ネイティブキャンバスの自動復元に失敗しました。手動で起動してください: first\nsecond",
        "ko-KR":
          "ComfyUI 네이티브 캔버스 자동 복원에 실패했습니다. 수동으로 켜 주세요: first\nsecond",
      },
    };

    const nonMatchingNodeSamples = [
      "节点 10(Foo) bar: baz",
      "节点 10(): missing input",
    ];

    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const [sample, expected] of Object.entries(expectedByMessage)) {
        const out = tb(sample);
        expect(out, `${loc}: ${sample}`).toBe(expected[loc]);
        if (loc !== "zh-CN") {
          expect(
            simplifiedLeakRe.test(out),
            `Simplified Chinese leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            leakRe.test(out),
            `CJK/fullwidth leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
        }
      }
      for (const sample of nonMatchingNodeSamples) {
        expect(
          tb(sample),
          `${loc} must not misparse non-matching node string "${sample}"`,
        ).toBe(sample);
      }
    }
  });
});

describe("ColabOne ComfyUI catalog & digital human backend i18n", () => {
  const CJK_OR_FULLWIDTH_RE =
    /[\u3400-\u9fff\uf900-\ufaff（）「」，。：；“”《》【】、～]/;
  const HAN_RE = /[\u3400-\u9fff\uf900-\ufaff]/;
  const SPOKEN_CANTONESE_RE = /[搵唔喺嘅啲咗冇]/;

  it("translates every user-facing string in catalog/comfyui.json cleanly across all 6 locales", () => {
    const entries: Array<{ path: string; text: string }> = [];

    for (const eng of comfyCatalog.tts_engines) {
      if (eng.name) {
        entries.push({ path: `tts_engines.${eng.id}.name`, text: eng.name });
      }
      if (eng.description) {
        entries.push({
          path: `tts_engines.${eng.id}.description`,
          text: eng.description,
        });
      }
      if (eng.license_notice) {
        entries.push({
          path: `tts_engines.${eng.id}.license_notice`,
          text: eng.license_notice,
        });
      }
    }

    for (const tpl of comfyCatalog.templates) {
      if (tpl.name) {
        entries.push({ path: `templates.${tpl.id}.name`, text: tpl.name });
      }
      if (tpl.description) {
        entries.push({
          path: `templates.${tpl.id}.description`,
          text: tpl.description,
        });
      }
      if (tpl.license_notice) {
        entries.push({
          path: `templates.${tpl.id}.license_notice`,
          text: tpl.license_notice,
        });
      }
      for (const field of tpl.fields) {
        if (field.label) {
          entries.push({
            path: `templates.${tpl.id}.fields.${field.key}.label`,
            text: field.label,
          });
        }
        if (field.description) {
          entries.push({
            path: `templates.${tpl.id}.fields.${field.key}.description`,
            text: field.description,
          });
        }
        for (const [optKey, optLabel] of Object.entries(
          field.option_labels || {},
        )) {
          if (typeof optLabel === "string" && optLabel) {
            entries.push({
              path: `templates.${tpl.id}.fields.${field.key}.option_labels.${optKey}`,
              text: optLabel,
            });
          }
        }
      }
      for (const [nodeId, node] of Object.entries(tpl.workflow || {})) {
        const title = (node as { _meta?: { title?: string } })?._meta?.title;
        if (title) {
          entries.push({
            path: `templates.${tpl.id}.workflow.${nodeId}._meta.title`,
            text: title,
          });
        }
      }
    }

    // Guard against silently vacuous catalog traversal
    expect(entries.length).toBeGreaterThanOrEqual(100);

    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const { path, text } of entries) {
        const out = translateColabOneBackendText(text, loc);
        expect(tb(text), `tb() parity for ${loc} ${path}`).toBe(out);
        expect(out.trim().length, `empty translation for ${loc} ${path}`).toBeGreaterThan(0);

        if (loc === "zh-CN") {
          expect(out, `zh-CN identity for ${path}`).toBe(text);
          continue;
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `CJK/fullwidth leak in ${loc} for ${path}: "${text}" -> "${out}"`,
          ).toBe(false);
          expect(
            out,
            `${loc} must translate ${path}: "${text}"`,
          ).not.toBe(text);
        }
        if (loc === "ja-JP" && HAN_RE.test(text)) {
          expect(
            out,
            `ja-JP must translate Simplified Chinese ${path}: "${text}"`,
          ).not.toBe(text);
        }
        if (loc === "zh-TW") {
          expect(
            out,
            `zh-TW Mainland VRAM leak in ${path}: "${out}"`,
          ).not.toMatch(/显存|顯存/);
        }
        if (loc === "zh-HK") {
          expect(
            out,
            `zh-HK spoken Cantonese in ${path}: "${out}"`,
          ).not.toMatch(SPOKEN_CANTONESE_RE);
        }
      }
    }

    // Spot-check user's exact Japanese screenshot strings
    expect(
      translateColabOneBackendText("口播数字人 · 长视频推荐版", "ja-JP"),
    ).toBe("デジタルヒューマン・長尺おすすめ版");
    expect(
      translateColabOneBackendText("口播数字人 · 旗舰高清版", "ja-JP"),
    ).toBe("デジタルヒューマン・フラッグシップ HD 版");
    expect(
      translateColabOneBackendText(
        "输入人像照片与口播文稿（或音频），自动生成口型自然、支持多分钟不崩脸的高清有声视频（MP4）。",
        "ja-JP",
      ),
    ).toContain("人物写真と台本");
    expect(
      translateColabOneBackendText(
        "VoxCPM2（OpenBMB · 48kHz 商用推荐）",
        "ja-JP",
      ),
    ).toBe("VoxCPM2（OpenBMB・48kHz 商用推奨）");
  });

  it("translates dynamic ComfyUI pack, WebSocket, TTS, digital human, and Go validation runtime messages across all 6 locales", () => {
    const runtimeSamples: string[] = [
      // === server/comfyui.py compound & WebSocket live progress ===
      "第 1/3 段 · 口播数字人 · 长视频推荐版 · InfiniteTalk 扩散采样器 · 采样 3/6",
      "第 2/3 段 · 口播数字人 · 旗舰高清版 · LongCat-Avatar 扩散采样器 · 采样 8/12",
      "第 1/3 段 · 口播数字人 · 长视频推荐版 · 远端 GPU 执行中 · 采样 3/6",
      "第 1/3 段 · 口播数字人 · 长视频推荐版 · 排队中（第 2 位）· 已用 14 秒",
      "第 1/3 段 · 提交工作流：口播数字人 · 长视频推荐版",
      "第 1/3 段 · 工作流已入队：口播数字人 · 长视频推荐版（任务 prompt-1234567890）",
      "第 1/3 段 · 工作流推理完成，正在保存 1 个输出作品",
      "ComfyUI 工作流：进行中 · 55% · 第 1/3 段 · 口播数字人 · 长视频推荐版 · InfiniteTalk 扩散采样器 · 采样 3/6",
      "自定义节点包已变更，正在重启远端 ComfyUI 引擎以加载新节点",

      // === server/comfy_ws.py ===
      "节点 15(WanVideoSampler): 工作流执行已中断",
      "节点 15: 工作流执行已中断",
      "工作流执行已中断",
      "工作流执行出错",

      // === server/comfy_packs.py (VRAM/disk preflight, custom nodes, pip, weights, symlinks) ===
      "当前远端实例未检测到可用 NVIDIA GPU，无法运行该工作流",
      "当前远端 GPU 显存不足：此工作流至少需要 40.0 GB 显存，当前 GPU 为 NVIDIA T4（15.0 GB），推荐切换至 G4 96 GB / A100 80 GB",
      "远端磁盘空间不足：至少需要 45.0 GiB 可用空间（当前仅剩 18.5 GiB），请清理其他模型或更换磁盘空间更大的远端实例",
      "复用远端已就绪的节点包：口播数字人 · 长视频推荐版",
      "准备自定义节点 · ComfyUI-WanVideoWrapper",
      "正在拉取节点包 ComfyUI-WanVideoWrapper（a7439db30b）",
      "安装节点依赖 · ComfyUI-WanVideoWrapper",
      "正在安装 ComfyUI-WanVideoWrapper 依赖（保留现有 CUDA PyTorch）",
      "安装工作流依赖包 · 口播数字人 · 长视频推荐版",
      "正在安装工作流额外依赖（7 个包）",
      "已应用节点兼容补丁：ComfyUI-WanVideoWrapper:wav2vec2_hidden_states_hook",
      "工作流节点包准备完成：口播数字人 · 长视频推荐版",
      "自定义节点目录 ComfyUI-WanVideoWrapper 已存在且非 ColabOne 托管，请手动移走后重试",
      "工作流所需的 8 个模型权重均已校验就绪，跳过下载",
      "准备下载工作流权重：8 个文件，共 26.57 GiB，16 线程",
      "下载模型 1/8 · Wan2_1-I2V-14B-480p_fp8_e4m3fn_scaled_KJ.safetensors",
      "模型 1/8 传输完成，正在校验 SHA-256",
      "模型 1/8 校验通过，已保存到远端：Wan2_1-I2V-14B-480p_fp8_e4m3fn_scaled_KJ.safetensors",
      "工作流权重全部已验证：26.57 GiB，用时 92.4 秒，平均传输 294.2 MiB/s；复用 128.0 MiB",

      // === server/tts_engines.py (isolated venv, pip, source, transcode, synthesis, VRAM, IndexTTS2) ===
      "未检测到可用远端 GPU，VoxCPM2（OpenBMB · 48kHz 商用推荐） 需要至少 8192 MiB 显存",
      "远端 TTS 引擎 IndexTTS-2（情绪分离控制 · 仅非商用） 需要至少 10240 MiB 显存，当前仅 8192 MiB",
      "复用已就绪的远端 TTS 引擎环境：VoxCPM2（OpenBMB · 48kHz 商用推荐）",
      "正在为远端 TTS 引擎创建隔离虚拟环境：VoxCPM2（OpenBMB · 48kHz 商用推荐）",
      "正在创建远端 TTS 独立虚拟环境：VoxCPM2（OpenBMB · 48kHz 商用推荐）",
      "正在安装远端 TTS 引擎依赖：VoxCPM2（OpenBMB · 48kHz 商用推荐）（共 4 个包，保留现有 PyTorch）",
      "正在拉取远端 TTS 引擎源码：IndexTTS-2（情绪分离控制 · 仅非商用）@d9e41aac89",
      "正在安装 IndexTTS-2（情绪分离控制 · 仅非商用） 源码依赖（保留现有 PyTorch）",
      "远端 TTS 引擎环境就绪：VoxCPM2（OpenBMB · 48kHz 商用推荐）",
      "正在将参考音频转码为标准 24 kHz PCM WAV：voice_ref.mp3",
      "开始远端 TTS 合成：VoxCPM2（OpenBMB · 48kHz 商用推荐）（共 3 段）",
      "正在远端合成语音…",
      "已完成第 2/3 句合成",
      "正在合成并标准化 48 kHz 母带音频…",
      "远端 TTS 合成完成：VoxCPM2（OpenBMB · 48kHz 商用推荐）（总时长 42.50 秒，48000 Hz）",
      "非商用许可提示：当前 TTS 引擎「IndexTTS-2（情绪分离控制 · 仅非商用）」采用 IndexTTS Research / Non-Commercial",
      "IndexTTS2 需要上传音色参考音频才能进行语音合成",
      "IndexTTS2 需要提供音色参考音频（ref_audio_path）",
      "VoxCPM2 当前版本克隆音色时需要同时填写参考音频对应文本",
      "远端 TTS 执行失败（退出码 1）：CUDA out of memory",
      "实例正在停止，已取消远端 TTS 任务",

      // === server/digital_human.py (pipeline orchestration, segmentation, muxing, errors) ===
      "请先输入数字人播报文案，或切换为上传成品音频模式",
      "正在释放 ComfyUI 显存以启动远端配音引擎",
      "正在使用远端 TTS 引擎（voxcpm2）合成高保真配音",
      "远端配音完成，音频总时长 62.4 秒",
      "正在预处理上传音频（48 kHz / -23 LUFS）",
      "音频预处理完成，总时长 62.4 秒，检测到 5 处自然换气静音点",
      "正在预处理主播参考图（832×480）",
      "正在准备远端视频环境与模型权重：口播数字人 · 长视频推荐版",
      "分段规划完成：共 2 段（总时长 62.4 秒，基础渲染 832×480，目标输出 1280×720 @ 25fps）",
      "口播数字人 · 长视频推荐版 · 正在渲染 第 1/2 段（0.0s–44.2s，1105 帧）",
      "正在进行最终视频拼接、分辨率精调与 48 kHz 高保真音频合成",
      "口播数字人 · 长视频推荐版 · 数字人长视频生成完成（共 2 段，62.4 秒，1280×720）",
      "第 2/2 段 下载的视频文件为空",
      "无法从第 1 段视频提取尾帧用于长视频衔接",
      "音视频处理失败（退出码 1）：ffmpeg exited",

      // === Symlink safety errors across comfy_packs.py, tts_engines.py, comfyui.py ===
      "ComfyUI 节点包根目录不能是符号链接",
      "自定义节点目录 ComfyUI-WanVideoWrapper 不能是符号链接",
      "节点包目录 digital-human-infinitetalk 不能是符号链接",
      "TTS 引擎目录不能是符号链接",
      "TTS 引擎虚拟环境目录不能是符号链接",
      "TTS 源码目录不能是符号链接",
      "TTS 工作目录不能是符号链接",
      "PyTorch 约束文件不能是符号链接",
      "参考音频转码目标不能是符号链接",
      "模型文件不能是符号链接：Wan2_1_VAE_bf16.safetensors",
      "模型临时文件不能是符号链接：Wan2_1_VAE_bf16.safetensors.part",
      "过滤后的依赖文件不能是符号链接：requirements.txt",
      "目标文件不能是符号链接：nodes.py",

      // === console/internal/comfyui/comfyui.go validation errors ===
      "工作流模板 digital-human-infinitetalk 引用了未定义的默认 TTS 引擎: bad_tts",
      "工作流模板 digital-human-infinitetalk 引用了未定义的 TTS 引擎: bad_tts",
      "节点补丁引用的自定义节点不存在: MissingNode",
      "内置工作流模板 ID 重复",
      "内置 TTS 引擎 ID 重复",
      "分段时长必须在 3 到 9 秒之间",
      "visible_when 条件 pipeline.audio_source 的值必须为字符串或布尔值",
      "非法的 TTS 引擎 ID: bad/id",
      "输出文件格式与类型不匹配：.gif",
    ];

    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const sample of runtimeSamples) {
        const out = translateColabOneBackendText(sample, loc);
        expect(tb(sample), `tb() parity for ${loc}: ${sample}`).toBe(out);

        if (loc === "zh-CN") {
          expect(out).toBe(sample);
          continue;
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `CJK/fullwidth leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
          expect(out, `${loc} must translate "${sample}"`).not.toBe(sample);
        }
        if (loc === "ja-JP") {
          expect(out, `ja-JP must translate "${sample}"`).not.toBe(sample);
        }
        if (loc === "zh-TW") {
          expect(
            out,
            `zh-TW Mainland VRAM leak in "${sample}" -> "${out}"`,
          ).not.toMatch(/显存|顯存/);
        }
        if (loc === "zh-HK") {
          expect(
            out,
            `zh-HK spoken Cantonese in "${sample}" -> "${out}"`,
          ).not.toMatch(SPOKEN_CANTONESE_RE);
        }
      }
    }

    // Exact multi-locale verification for the compound segment + sampler progress line
    const compoundSample =
      "第 1/3 段 · 口播数字人 · 长视频推荐版 · InfiniteTalk 扩散采样器 · 采样 3/6";
    const expectedCompound: Record<ColabOneLocale, string> = {
      "zh-CN":
        "第 1/3 段 · 口播数字人 · 长视频推荐版 · InfiniteTalk 扩散采样器 · 采样 3/6",
      "en-US":
        "Segment 1/3 · Talking Avatar · Long-Video Recommended · InfiniteTalk Diffusion Sampler · Step 3/6",
      "zh-TW":
        "第 1/3 段 · 口播數位人 · 長影片推薦版 · InfiniteTalk 擴散取樣器 · 取樣 3/6",
      "zh-HK":
        "第 1/3 段 · 口播數碼人 · 長影片推薦版 · InfiniteTalk 擴散採樣器 · 採樣 3/6",
      "ja-JP":
        "セグメント 1/3 · デジタルヒューマン・長尺おすすめ版 · InfiniteTalk 拡散サンプラー · ステップ 3/6",
      "ko-KR":
        "세그먼트 1/3 · 토킹 아바타 · 장편 영상 추천 · InfiniteTalk 확산 샘플러 · 스텝 3/6",
    };
    for (const loc of ALL_LOCALES) {
      expect(
        translateColabOneBackendText(compoundSample, loc),
        `compound progress for ${loc}`,
      ).toBe(expectedCompound[loc]);
    }

    // Exact multi-locale verification for IndexTTS2 reference-audio requirement
    const indexTtsMsg = "IndexTTS2 需要上传音色参考音频才能进行语音合成";
    expect(translateColabOneBackendText(indexTtsMsg, "en-US")).toContain(
      "IndexTTS2",
    );
    expect(translateColabOneBackendText(indexTtsMsg, "ja-JP")).toContain(
      "IndexTTS2",
    );
    expect(translateColabOneBackendText(indexTtsMsg, "ja-JP")).not.toBe(
      indexTtsMsg,
    );
    expect(translateColabOneBackendText(indexTtsMsg, "ko-KR")).toContain(
      "IndexTTS2",
    );
  });

  it("translates Round-2 C1/C2 progress, middot-preserving failure splits, and HIGH backend messages across all 6 locales with zero leaks or forbidden zh-TW terms", () => {
    const FORBIDDEN_ZH_TW_RE = /工作流(?!程)|显存|顯存|尾帧|尾幀|渲染/;

    const strictSamples: string[] = [
      // Round-2 C1 & C2 compound progress with middot workflow name
      "ComfyUI 工作流：进行中 · 45% · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 远端 GPU 执行中 · 已用 28 秒",
      "ComfyUI 工作流：进行中 · 30% · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 排队中（第 1 位）· 已用 12 秒",
      // Failure split without slicing middot workflow name (real backend error detail forms)
      "ComfyUI 工作流：失败 · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 无法从第 1 段视频提取尾帧用于长视频衔接：音视频处理命令失败（退出码 1）",
      "ComfyUI 工作流：失败 · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 无法从第 1 段视频提取尾帧用于长视频衔接：退出码 1",
      "ComfyUI 工作流：失败 · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 无法从第 1 段视频提取尾帧用于长视频衔接",
      // Round-2 HIGH backend messages
      "节点已完成",
      "ComfyUI 工作流：进行中 · 80% · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 节点已完成",
      "远端已受理 ComfyUI 工作流任务，但刷新状态失败（将自动重试）：timeout",
      "第 1/2 段 执行完成但未产出有效视频文件",
      "第 1/2 段 返回了图片而非视频文件，请检查视频输出节点格式",
      "单段最大时长 参数格式无效：不能为布尔值",
      "尾帧偏移时长 必须为有限数值",
      "自定义节点符号链接 ComfyUI-Foo 已存在且非 ColabOne 托管，请手动移走后重试",
      "模型临时路径不能是目录：foo.part",
      "模型目标路径已存在同名目录：foo.safetensors",
      "输出缓存文件不能是符号链接",
      "工作流 JSON 嵌套层级超过 32 层上限",
      "hf_token 包含非法空白或控制字符",
      "自定义工作流模板不允许声明远端依赖包、模型下载或流水线配置",
      "Qwen3-TTS 缺少音色参考音频或 VoiceDesign 辅助模型配置",
      "单连接下载失败（InvalidHeader）",
    ];

    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const sample of strictSamples) {
        const out = translateColabOneBackendText(sample, loc);
        expect(tb(sample), `tb() parity for ${loc}: ${sample}`).toBe(out);
        expect(out.trim().length, `empty output for ${loc}: ${sample}`).toBeGreaterThan(0);

        if (loc === "zh-CN") {
          expect(out).toBe(sample);
          continue;
        }
        expect(out, `${loc} must translate "${sample}"`).not.toBe(sample);
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `Han/fullwidth leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
        }
        if (loc === "zh-TW") {
          expect(
            out,
            `forbidden zh-TW term in "${sample}" -> "${out}"`,
          ).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(
            out,
            `zh-HK spoken Cantonese in "${sample}" -> "${out}"`,
          ).not.toMatch(SPOKEN_CANTONESE_RE);
        }
      }
    }

    // Exact 6-locale assertions for C1, C2, and middot-preserving failure split
    const exactCases: Record<string, Record<ColabOneLocale, string>> = {
      "ComfyUI 工作流：进行中 · 45% · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 远端 GPU 执行中 · 已用 28 秒":
        {
          "zh-CN":
            "ComfyUI 工作流：进行中 · 45% · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 远端 GPU 执行中 · 已用 28 秒",
          "en-US":
            "ComfyUI workflow: Running · 45% · Segment 1/3 · Talking Avatar · Long-Video Recommended · Running on remote GPU · 28s elapsed",
          "zh-TW":
            "ComfyUI 工作流程：進行中 · 45% · 第 1/3 段 · 口播數位人 · 長影片推薦版 · 遠端 GPU 執行中 · 已用 28 秒",
          "zh-HK":
            "ComfyUI 工作流：進行中 · 45% · 第 1/3 段 · 口播數碼人 · 長影片推薦版 · 遠端 GPU 執行中 · 已用 28 秒",
          "ja-JP":
            "ComfyUI ワークフロー: 実行中 · 45% · セグメント 1/3 · デジタルヒューマン・長尺おすすめ版 · リモート GPU で実行中 · 28 秒経過",
          "ko-KR":
            "ComfyUI 워크플로: 진행 중 · 45% · 세그먼트 1/3 · 토킹 아바타 · 장편 영상 추천 · 원격 GPU에서 실행 중 · 28초 경과",
        },
      "ComfyUI 工作流：进行中 · 30% · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 排队中（第 1 位）· 已用 12 秒":
        {
          "zh-CN":
            "ComfyUI 工作流：进行中 · 30% · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 排队中（第 1 位）· 已用 12 秒",
          "en-US":
            "ComfyUI workflow: Running · 30% · Segment 1/3 · Talking Avatar · Long-Video Recommended · Queued (#1) · 12s elapsed",
          "zh-TW":
            "ComfyUI 工作流程：進行中 · 30% · 第 1/3 段 · 口播數位人 · 長影片推薦版 · 排隊中（第 1 位）· 已用 12 秒",
          "zh-HK":
            "ComfyUI 工作流：進行中 · 30% · 第 1/3 段 · 口播數碼人 · 長影片推薦版 · 排隊中（第 1 位）· 已用 12 秒",
          "ja-JP":
            "ComfyUI ワークフロー: 実行中 · 30% · セグメント 1/3 · デジタルヒューマン・長尺おすすめ版 · キュー待機中（1 番目）· 12 秒経過",
          "ko-KR":
            "ComfyUI 워크플로: 진행 중 · 30% · 세그먼트 1/3 · 토킹 아바타 · 장편 영상 추천 · 대기 중 (1번째) · 12초 경과",
        },
      "ComfyUI 工作流：失败 · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 无法从第 1 段视频提取尾帧用于长视频衔接：音视频处理命令失败（退出码 1）":
        {
          "zh-CN":
            "ComfyUI 工作流：失败 · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 无法从第 1 段视频提取尾帧用于长视频衔接：音视频处理命令失败（退出码 1）",
          "en-US":
            "ComfyUI workflow: Failed · Segment 1/3 · Talking Avatar · Long-Video Recommended · Unable to extract tail frame from video segment 1 for long-video continuation: Audio/video processing command failed (exit code 1)",
          "zh-TW":
            "ComfyUI 工作流程：失敗 · 第 1/3 段 · 口播數位人 · 長影片推薦版 · 無法從第 1 段影片擷取末影格以進行長影片銜接：影音處理命令失敗（結束代碼 1）",
          "zh-HK":
            "ComfyUI 工作流：失敗 · 第 1/3 段 · 口播數碼人 · 長影片推薦版 · 無法從第 1 段影片提取尾幀用於長影片銜接：音影片處理命令失敗（退出碼 1）",
          "ja-JP":
            "ComfyUI ワークフロー: 失敗 · セグメント 1/3 · デジタルヒューマン・長尺おすすめ版 · 長尺動画の連結用に第 1 セグメント動画から末尾フレームを抽出できませんでした: 音声・動画処理コマンドが失敗しました（終了コード 1）",
          "ko-KR":
            "ComfyUI 워크플로: 실패 · 세그먼트 1/3 · 토킹 아바타 · 장편 영상 추천 · 장편 영상 연결을 위해 1번째 영상 구간에서 마지막 프레임을 추출할 수 없습니다: 오디오/비디오 처리 명령 실패 (종료 코드 1)",
        },
    };

    for (const loc of ALL_LOCALES) {
      for (const [sample, expectedByLoc] of Object.entries(exactCases)) {
        expect(
          translateColabOneBackendText(sample, loc),
          `exact Round-2 translation for ${loc}: ${sample}`,
        ).toBe(expectedByLoc[loc]);
      }
    }

    // Failure split with arbitrary detail suffix preserves intact middot workflow name across all 6 locales
    const arbitraryDetailFailure =
      "ComfyUI 工作流：失败 · 第 1/3 段 · 口播数字人 · 长视频推荐版 · 无法从第 1 段视频提取尾帧用于长视频衔接：ffmpeg 退出码 1";
    const expectedArbitraryDetailFailure: Record<ColabOneLocale, string> = {
      "zh-CN": arbitraryDetailFailure,
      "en-US":
        "ComfyUI workflow: Failed · Segment 1/3 · Talking Avatar · Long-Video Recommended · Unable to extract tail frame from video segment 1 for long-video continuation: ffmpeg 退出码 1",
      "zh-TW":
        "ComfyUI 工作流程：失敗 · 第 1/3 段 · 口播數位人 · 長影片推薦版 · 無法從第 1 段影片擷取末影格以進行長影片銜接：ffmpeg 退出码 1",
      "zh-HK":
        "ComfyUI 工作流：失敗 · 第 1/3 段 · 口播數碼人 · 長影片推薦版 · 無法從第 1 段影片提取尾幀用於長影片銜接：ffmpeg 退出码 1",
      "ja-JP":
        "ComfyUI ワークフロー: 失敗 · セグメント 1/3 · デジタルヒューマン・長尺おすすめ版 · 長尺動画の連結用に第 1 セグメント動画から末尾フレームを抽出できませんでした: ffmpeg 退出码 1",
      "ko-KR":
        "ComfyUI 워크플로: 실패 · 세그먼트 1/3 · 토킹 아바타 · 장편 영상 추천 · 장편 영상 연결을 위해 1번째 영상 구간에서 마지막 프레임을 추출할 수 없습니다: ffmpeg 退出码 1",
    };
    for (const loc of ALL_LOCALES) {
      const out = translateColabOneBackendText(arbitraryDetailFailure, loc);
      expect(out, `failure split for ${loc}`).toBe(
        expectedArbitraryDetailFailure[loc],
      );
      if (loc === "en-US" || loc === "ko-KR") {
        const prefixBeforeRawDetail = out.replace(/: ffmpeg 退出码 1$/, "");
        expect(
          CJK_OR_FULLWIDTH_RE.test(prefixBeforeRawDetail),
          `Han/fullwidth leak in ${loc} failure prefix: "${prefixBeforeRawDetail}"`,
        ).toBe(false);
      }
      if (loc === "zh-TW") {
        expect(out).not.toMatch(FORBIDDEN_ZH_TW_RE);
      }
    }
  });

  it("provides localized ComfyUI default visual prompt, input placeholders, draft status keys, and multilingual prompt description across all 6 locales", () => {
    const FORBIDDEN_ZH_TW_RE =
      /工作流(?!程)|显存|顯存|尾帧|尾幀|渲染|视频|視頻|音频|音頻|默认|保存|缓存|緩存/;
    const localizedPromptAndDraftKeys: ColabOneMessageKey[] = [
      "comfy.defaultVisualPrompt",
      "comfy.placeholderScriptText",
      "comfy.placeholderVoiceRefText",
      "comfy.placeholderVoicePrompt",
      "comfy.placeholderEmotionPrompt",
      "comfy.placeholderVisualPrompt",
      "comfy.placeholderNegativePrompt",
      "comfy.placeholderGenericPrompt",
      "comfy.placeholderGenericText",
      "comfy.draftSavedBadge",
      "comfy.draftSavingBadge",
      "comfy.draftRestoredBadge",
      "comfy.draftLocalMediaTag",
      "comfy.errSaveDraft",
    ];

    for (const key of localizedPromptAndDraftKeys) {
      const seenValues = new Set<string>();
      for (const loc of ALL_LOCALES) {
        const val = COLABONE_MESSAGES[loc][key];
        expect(val.trim().length, `empty ${loc}[${key}]`).toBeGreaterThan(0);
        seenValues.add(val);

        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(val),
            `CJK/fullwidth leak in ${loc}[${key}]: "${val}"`,
          ).toBe(false);
        }
        if (loc === "zh-TW") {
          expect(
            val,
            `forbidden zh-TW term in ${key}: "${val}"`,
          ).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(
            val,
            `spoken Cantonese leak in zh-HK[${key}]: "${val}"`,
          ).not.toMatch(SPOKEN_CANTONESE_RE);
        }
      }
      // Every non-CJK locale must differ from zh-CN
      for (const loc of ["en-US", "ja-JP", "ko-KR"] as const) {
        expect(
          COLABONE_MESSAGES[loc][key],
          `${loc}[${key}] must be localized`,
        ).not.toBe(COLABONE_MESSAGES["zh-CN"][key]);
      }
      expect(
        seenValues.size,
        `${key} should have distinct localized strings`,
      ).toBeGreaterThanOrEqual(5);
    }

    // All 6 locales provide a distinct default visual prompt, and en-US matches catalog canonical fallback
    const defaultVisualPrompts = ALL_LOCALES.map(
      (loc) => COLABONE_MESSAGES[loc]["comfy.defaultVisualPrompt"],
    );
    expect(new Set(defaultVisualPrompts).size).toBe(ALL_LOCALES.length);
    for (const tpl of comfyCatalog.templates) {
      if (
        tpl.id === "digital-human-longcat" ||
        tpl.pipeline?.video_backend === "longcat_avatar_native"
      ) {
        const posField = tpl.fields.find(
          (f) => f.key === "pipeline.positive_prompt",
        );
        expect(
          posField,
          `${tpl.id} must have pipeline.positive_prompt`,
        ).toBeTruthy();
        expect(posField?.default).toBe("");
        expect(posField?.description).toBe(
          "留空则按上方「动作与景别风格」自动生成最佳提示词；填写后将覆盖默认提示词。",
        );
      } else {
        const posField = tpl.fields.find((f) => f.key === "5.positive_prompt");
        expect(posField, `${tpl.id} must have 5.positive_prompt`).toBeTruthy();
        expect(posField?.default).toBe(
          COLABONE_MESSAGES["en-US"]["comfy.defaultVisualPrompt"],
        );
        expect(posField?.description).toBe(
          "描述主播说话时的神态、头部微动、光线与镜头风格（支持中英文等多语种自然描述）。",
        );
      }
    }

    // Updated multilingual prompt description and draft backend messages translate cleanly across all 6 locales
    const updatedDescription =
      "描述主播说话时的神态、头部微动、光线与镜头风格（支持中英文等多语种自然描述）。";
    const legacyDescription =
      "描述主播说话时的神态、头部微动、光线与镜头风格（建议使用英文短语获得最佳稳定性）。";
    const draftBackendMessages = [
      updatedDescription,
      legacyDescription,
      "草稿素材不存在",
      "读取本地 ComfyUI 草稿配置失败，已保留原文件未覆盖",
      "草稿字段键不合法",
      "草稿文本字段超过 200000 字节上限",
      "草稿字段数值不合法",
      "草稿字段仅支持文本、数值或布尔值",
      "草稿字段必须为对象",
      "草稿字段数量不能超过 128 个",
      "草稿素材类型仅支持 image 或 audio",
      "草稿目录未配置",
      "草稿目录不安全",
      "草稿元数据路径不安全",
      "草稿素材 ID 不合法",
      "草稿素材大小信息不合法",
      "草稿素材校验信息不合法",
      "草稿素材路径不安全",
      "草稿素材文件已丢失",
      "草稿素材大小超出允许范围，文件可能已损坏",
      "草稿素材大小校验失败，文件可能已损坏",
      "草稿素材哈希校验失败，文件可能已损坏",
      "不支持的图片输入文件格式",
      "不支持的音频输入文件格式",
      "不支持的图片输入文件格式：mp3",
      "不支持的音频输入文件格式：png",
    ];
    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const msg of draftBackendMessages) {
        const out = translateColabOneBackendText(msg, loc);
        expect(tb(msg), `tb() parity for ${loc}: ${msg}`).toBe(out);
        expect(out.trim().length, `empty translation for ${loc}: ${msg}`).toBeGreaterThan(0);
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `CJK/fullwidth leak in ${loc} for "${msg}" -> "${out}"`,
          ).toBe(false);
          expect(out).not.toBe(msg);
        }
        if (loc === "ja-JP") {
          expect(out).not.toBe(msg);
        }
        if (loc === "zh-TW") {
          expect(out).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(out).not.toMatch(SPOKEN_CANTONESE_RE);
        }
      }
    }
  });

  it("translates human-friendly task elapsed duration keys and backend logs across all 6 locales", () => {
    const FORBIDDEN_ZH_TW_RE =
      /工作流(?!程)|显存|顯存|尾帧|尾幀|渲染|视频|視頻|音频|音頻|默认|保存|缓存|緩存/;
    const durationKeys: ColabOneMessageKey[] = [
      "common.minutesSeconds",
      "common.hoursMinutesSeconds",
      "task.elapsedLive",
      "task.elapsedTotal",
      "comfy.noticeRunCompleted",
      "comfy.noticeRunCompletedNoDuration",
    ];
    for (const key of durationKeys) {
      for (const loc of ALL_LOCALES) {
        const val = COLABONE_MESSAGES[loc][key];
        expect(val.trim().length, `empty ${loc}[${key}]`).toBeGreaterThan(0);
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(val),
            `CJK/fullwidth leak in ${loc}[${key}]: "${val}"`,
          ).toBe(false);
        }
        if (loc === "zh-TW") {
          expect(val, `forbidden zh-TW term in ${key}: "${val}"`).not.toMatch(
            FORBIDDEN_ZH_TW_RE,
          );
        }
        if (loc === "zh-HK") {
          expect(val, `spoken Cantonese leak in zh-HK[${key}]: "${val}"`).not.toMatch(
            SPOKEN_CANTONESE_RE,
          );
        }
      }
    }

    const durationBackendLogs = [
      "ComfyUI 工作流：已完成 · 耗时 1 小时 15 分 23 秒 · 工作流执行完成，已保存 1 个作品到远端 GPU（耗时 1 小时 15 分 23 秒）",
      "ComfyUI 工作流：已取消 · 耗时 12 分 34 秒",
      "工作流执行完成，已保存 2 个作品到远端 GPU（耗时 3 分 18 秒）",
      "口播数字人 · 旗舰高清版 · 第 3/6 段 渲染完成（耗时 18 分 42 秒）",
      "口播数字人 · 旗舰高清版 · 数字人长视频生成完成（共 6 段，33.0 秒，1280×720，总耗时 1 小时 52 分 10 秒）",
      "口播数字人 · 旗舰高清版 · 远端 GPU 执行中 · 已用 14 分 5 秒",
      "Flux Demo · 排队中（第 1 位）· 已用 1 小时 2 分 9 秒",
      "Flux Demo · 远端 GPU 执行中 · 已用 3725 秒",
    ];

    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const msg of durationBackendLogs) {
        const out = translateColabOneBackendText(msg, loc);
        expect(tb(msg), `tb() parity for ${loc}: ${msg}`).toBe(out);
        expect(out.trim().length, `empty translation for ${loc}: ${msg}`).toBeGreaterThan(0);
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `CJK/fullwidth leak in ${loc} for "${msg}" -> "${out}"`,
          ).toBe(false);
          expect(out).not.toBe(msg);
        }
        if (loc === "ja-JP") {
          expect(out).not.toBe(msg);
        }
        if (loc === "zh-TW") {
          expect(out).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(out).not.toMatch(SPOKEN_CANTONESE_RE);
        }
      }
    }

    // Spot-check exact human-friendly English formatting (including legacy "已用 3725 秒" promotion to 1h 2m 5s)
    expect(
      translateColabOneBackendText(
        "ComfyUI 工作流：已完成 · 耗时 1 小时 15 分 23 秒",
        "en-US",
      ),
    ).toBe("ComfyUI workflow: Completed · took 1h 15m 23s");
    expect(
      translateColabOneBackendText(
        "Flux Demo · 远端 GPU 执行中 · 已用 3725 秒",
        "en-US",
      ),
    ).toBe("Flux Demo · Running on remote GPU · 1h 2m 5s elapsed");
    expect(
      translateColabOneBackendText(
        "口播数字人 · 旗舰高清版 · 第 3/6 段 渲染完成（耗时 18 分 42 秒）",
        "en-US",
      ),
    ).toBe(
      "Talking Avatar · Flagship HD · Segment 3/6 rendered (took 18m 42s)",
    );
  });

  it("translates ComfyUI Google Drive direct export backend messages across all 6 locales", () => {
    const FORBIDDEN_ZH_TW_RE =
      /工作流(?!程)|显存|顯存|尾帧|尾幀|渲染|视频|視頻|音频|音頻|默认|保存|缓存|緩存/;
    const driveExportBackendMsgs = [
      "当前实例尚未连接 Google 云盘，请先完成云盘授权与挂载",
      "当前远端服务版本暂不支持作品直传 Google 云盘，请先点击顶部「立即热更新」升级远端服务后再试",
      "正在将 ComfyUI 作品直传导出至 Google 云盘 (ColabOne_Drive/outputs)",
      "Google 云盘连接已断开，请重新挂载后再试",
      "未检测到云盘凭据配置，请重新挂载 Google 云盘",
      "导出到 Google 云盘超时，请稍后重试",
      "已将作品导出至 Google 云盘：My Drive / ColabOne_Drive/outputs / avatar_720x1280.mp4",
      "已将作品导出至 Google 云盘 (ColabOne_Drive/outputs)",
      "Google 云盘导出目录不可读写：Permission denied",
      "写入 Google 云盘失败：I/O error",
      "导出到 Google 云盘失败：network reset",
    ];
    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const msg of driveExportBackendMsgs) {
        const out = translateColabOneBackendText(msg, loc);
        expect(tb(msg), `tb() parity for ${loc}: ${msg}`).toBe(out);
        expect(out.trim().length, `empty translation for ${loc}: ${msg}`).toBeGreaterThan(0);
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `CJK/fullwidth leak in ${loc} for "${msg}" -> "${out}"`,
          ).toBe(false);
          expect(out).not.toBe(msg);
        }
        if (loc === "ja-JP") {
          expect(out).not.toBe(msg);
        }
        if (loc === "zh-TW") {
          expect(out).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(out).not.toMatch(SPOKEN_CANTONESE_RE);
        }
      }
    }
  });

  it("translates LongCat acceleration options, built-in female voice presets, and dynamic acceleration/voice logs across all 6 locales with zero leaks", () => {
    const FORBIDDEN_ZH_TW_RE =
      /工作流(?!程)|显存|顯存|尾帧|尾幀|渲染|视频|視頻|音频|音頻|默认|保存|缓存|緩存/;
    const STRICT_CANTONESE_RE = /[搵唔喺嘅啲咗冇佢咁]/;

    const staticCatalogAndValidationStrings = [
      // Updated digital-human-longcat description
      "输入人像照片与口播文稿（或音频），默认采用官方 480p 极速扩散 + AI 2x 细节超分与全栈 GPU 加速，并支持一键切换原生 720p 极致细节。",
      // pipeline.voice_preset label, description, 6 option_labels
      "内置女声音色",
      "为所有远端配音引擎内置 5 位好听的中文女声；若上传了参考音色音频，将优先克隆所传音色。",
      "沈知意 · 知性女主播（清澈温润 · 默认推荐）",
      "林小满 · 甜美活力（清亮灵动 · 种草口播）",
      "苏晚晴 · 温柔治愈（柔和细腻 · 故事解说）",
      "顾清岚 · 干练职场（醇厚沉稳 · 商业科技）",
      "陆明薇 · 播音气质（字正腔圆 · 资讯讲解）",
      "自定义音色（使用下方参考音色或文本捏声线）",
      // 5 short voice names & styles
      "知性女主播 · 沈知意",
      "甜美活力 · 林小满",
      "温柔治愈 · 苏晚晴",
      "干练职场 · 顾清岚",
      "播音气质 · 陆明薇",
      "知性主播",
      "甜美活力",
      "温柔治愈",
      "干练御姐",
      "新闻播音",
      // Updated pipeline.voice_ref_audio description
      "上传 5～15 秒清晰人声即可克隆该音色（优先级高于内置女声）；留空则自动使用上方所选内置女声（IndexTTS-2 仅在选用「自定义音色」时必须上传）。",
      // pipeline.render_quality label, description, 2 option_labels
      "渲染清晰度与速度模式",
      "官方 480p 极速扩散 + AI 2x 细节超分（默认推荐 · 极快）",
      "原生 720p 极致细节（1280×768 扩散 · 细节上限但较慢）",
      "默认采用美团与 Kijai 官方 480p（832×480）扩散叠加 AI 2x 细节超分，速度提升数倍且保留精准口型；切至原生 720p 可追求极限单帧纹理。",
      // pipeline.acceleration_profile label, description, 3 option_labels
      "远端 GPU 加速栈",
      "极致全栈加速（SageAttention 2/3 + FP8 Fast + 常驻显存 + torch.compile · 推荐）",
      "高速稳健（SageAttention + FP8 Fast + 常驻显存，跳过编译预热）",
      "兼容保底（原生 BF16 + SDPA，关闭底层算子加速）",
      "自动按远端 GPU 架构启用最强算子组合，并将 SageAttention 编译产物缓存至云盘复用；任一项不兼容时自动安全回退。",
      // Updated pipeline.upscale_mode description & option_labels
      "默认配合 480p 扩散开启 2xNomosUni AI 细节超分输出高清 720p/1080p；若切至原生 720p 扩散，可选 Lanczos 精准适配画幅。",
      "AI 2x 细节超分（2xNomosUni SPAN · 推荐高清）",
      "Lanczos 高质量适配（精准输出所选画幅）",
      "原生渲染尺寸输出（不缩放，保留原生画幅）",
      // Updated pipeline.max_segment_seconds & 14.steps descriptions
      "LongCat-Avatar 1.5 原生黄金窗口为 93 帧（约 3.7 秒 @ 25fps），默认 3.7 秒可避开二次方注意力耗时激增，并自动接入原图参考锁脸防漂移。",
      "配合 LongCat-Avatar 1.5 DMD 蒸馏 LoRA 官方推荐 8 步，兼顾极速采样与精准口型细节（可按需在 6～12 步微调）。",
      // Schema & TTS validation strings
      "分段时长必须在 3.0 到 300.0 秒之间",
      "IndexTTS2 在自定义音色模式下需要上传音色参考音频才能进行语音合成",
    ];

    const dynamicOptimizationLogs = [
      "已启用 LongCat 远端加速栈：注意力=sageattn_3，量化=fp8_e4m3fn_fast，显存常驻=是，torch.compile=开",
      "已启用 LongCat 远端加速栈：注意力=sdpa，量化=disabled，显存常驻=否，torch.compile=关",
      "复用已缓存的 SageAttention 2 加速包（sageattention-2.2.0-sm89.whl）",
      "复用已缓存的 SageAttention 3 加速包（sm120）",
      "正在为远端 GPU（NVIDIA L4 · sm89）编译 SageAttention 2 加速包（首次需 1～3 分钟，完成后自动缓存复用）",
      "正在为远端 GPU（NVIDIA RTX PRO 6000 · sm120）编译 SageAttention 3 加速包（首次需 1～3 分钟，完成后自动缓存复用）",
      "SageAttention 加速包构建或安装跳过，将自动回退：nvcc not found",
      "LongCat 加速算子执行异常，自动回退至高速稳健（关闭 torch.compile）：InductorError",
      "LongCat 加速算子执行异常，自动回退至兼容保底（原生 BF16 + SDPA）：CUDA error",
      "LongCat 加速算子执行异常，自动回退至 SageAttention 2：unsupported sm",
      "复用已缓存的内置女声参考样本：知性女主播 · 沈知意",
      "正在生成并缓存内置女声参考样本：甜美活力 · 林小满",
      "ComfyUI 工作流：进行中 · 20% · 正在生成并缓存内置女声参考样本：知性女主播 · 沈知意",
      "ComfyUI 工作流：进行中 · 35% · 已启用 LongCat 远端加速栈：注意力=sageattn，量化=fp8_e4m3fn_fast，显存常驻=是，torch.compile=开",
    ];

    const allSamples = [
      ...staticCatalogAndValidationStrings,
      ...dynamicOptimizationLogs,
    ];

    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const sample of allSamples) {
        const out = translateColabOneBackendText(sample, loc);
        expect(tb(sample), `tb() parity for ${loc}: ${sample}`).toBe(out);
        expect(
          out.trim().length,
          `empty translation for ${loc}: ${sample}`,
        ).toBeGreaterThan(0);

        if (loc === "zh-CN") {
          expect(out).toBe(sample);
          continue;
        }
        if (loc !== "ja-JP") {
          expect(
            out,
            `katakana middot leak in ${loc} for "${sample}" -> "${out}"`,
          ).not.toContain("\u30fb");
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `CJK/fullwidth leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
          expect(out, `${loc} must translate "${sample}"`).not.toBe(sample);
        }
        if (loc === "ja-JP") {
          expect(out, `ja-JP must translate "${sample}"`).not.toBe(sample);
        }
        if (loc === "zh-TW") {
          expect(
            out,
            `forbidden zh-TW term in "${sample}" -> "${out}"`,
          ).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(
            out,
            `spoken Cantonese leak in zh-HK for "${sample}" -> "${out}"`,
          ).not.toMatch(STRICT_CANTONESE_RE);
        }
      }
    }

    // Spot-check exact multi-locale outputs for key dynamic logs & presets
    expect(
      translateColabOneBackendText(
        "已启用 LongCat 远端加速栈：注意力=sageattn_3，量化=fp8_e4m3fn_fast，显存常驻=是，torch.compile=开",
        "en-US",
      ),
    ).toBe(
      "Enabled LongCat remote acceleration stack: attention=sageattn_3, quantization=fp8_e4m3fn_fast, resident VRAM=yes, torch.compile=on",
    );
    expect(
      translateColabOneBackendText(
        "已启用 LongCat 远端加速栈：注意力=sageattn_3，量化=fp8_e4m3fn_fast，显存常驻=是，torch.compile=开",
        "zh-TW",
      ),
    ).toBe(
      "已啟用 LongCat 遠端加速堆疊：注意力=sageattn_3，量化=fp8_e4m3fn_fast，顯示記憶體常駐=是，torch.compile=開",
    );
    expect(
      translateColabOneBackendText(
        "复用已缓存的内置女声参考样本：知性女主播 · 沈知意",
        "en-US",
      ),
    ).toBe(
      "Reusing cached built-in female voice reference sample: Intellectual Host · Shen Zhiyi",
    );
    expect(
      translateColabOneBackendText(
        "LongCat 加速算子执行异常，自动回退至高速稳健（关闭 torch.compile）：InductorError",
        "en-US",
      ),
    ).toBe(
      "LongCat accelerated kernel failed; automatically falling back to Fast & Stable (torch.compile off): InductorError",
    );
    expect(
      translateColabOneBackendText(
        "正在为远端 GPU（NVIDIA L4 · sm89）编译 SageAttention 2 加速包（首次需 1～3 分钟，完成后自动缓存复用）",
        "ko-KR",
      ),
    ).toBe(
      "원격 GPU (NVIDIA L4 · sm89)용 SageAttention 2 가속 패키지 컴파일 중 (최초 1-3분 소요, 완료 후 자동 캐시 재사용)",
    );

    // Verify all runtime acceleration logs, injected node titles, and Go voice_presets validation errors
    const extraAuditSamples = [
      "torch.compile 算子加速",
      "原始人像参考图（锁脸）",
      "原始人像参考潜变量",
      "已安装 SageAttention 加速算子，正在重启远端 ComfyUI 引擎以加载新内核",
      "正在为 sm_120 构建 SageAttention 2 加速包（首次构建后将缓存复用）",
      "正在为 Blackwell sm_120 构建 SageAttention 3 加速包（首次构建后将缓存复用）",
      "构建 SageAttention 2 加速包 · sm_120",
      "SageAttention 2 加速包构建并安装完成（sm_120）",
      "当前 GPU 计算能力为 sm_75（低于 sm_80），跳过 SageAttention 加速包安装",
      "未检测到满足 sm_120 要求的 nvcc（需要 CUDA >= 12.8），跳过 SageAttention 2 源码构建",
      "未检测到满足 Blackwell sm_120 要求的 nvcc（需要 CUDA >= 12.8），跳过 SageAttention 3 源码构建",
      "当前 PyTorch 2.7.1 低于 2.8，跳过 SageAttention 3 源码构建（已启用 SageAttention 2）",
      "准备 SageAttention 算子时出现非致命异常，将自动使用当前可用注意力实现：nvcc failed",
      "第 1/3 段 加速算子执行异常（InductorError），已自动关闭 torch.compile 编译加速（回退至高速稳健模式）并重试",
      "第 1/3 段 检测到远端 ComfyUI 已退出，正在重启引擎后继续回退重试",
      "缺少 VoxCPM2 引擎配置，无法生成内置女声参考样本：知性女主播 · 沈知意",
      "内置女声参考样本生成失败：知性女主播 · 沈知意",
      "内置女声缓存目录不能是符号链接",
      "加速包缓存目录不能是符号链接",
      "内置音色 ID 不合法",
      "内置音色名称长度不合法",
      "内置音色名称不能为空",
      "内置音色性别不合法",
      "内置音色风格长度不能超过 80",
      "内置音色描述长度不能超过 240",
      "内置音色提示词长度不能超过 600",
      "内置音色随机种子必须在 0 到 2147483647 之间",
      "内置音色预设数量不能超过 24 个",
    ];
    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const sample of extraAuditSamples) {
        const out = translateColabOneBackendText(sample, loc);
        if (loc === "zh-CN") {
          expect(out).toBe(sample);
          continue;
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `CJK/fullwidth leak in ${loc} for "${sample}" -> "${out}"`,
          ).toBe(false);
          expect(out, `${loc} must translate "${sample}"`).not.toBe(sample);
        }
        if (loc === "zh-TW") {
          expect(out, `forbidden zh-TW term in "${sample}" -> "${out}"`).not.toMatch(
            FORBIDDEN_ZH_TW_RE,
          );
        }
        if (loc === "zh-HK") {
          expect(out, `spoken Cantonese leak in zh-HK for "${sample}" -> "${out}"`).not.toMatch(
            STRICT_CANTONESE_RE,
          );
        }
      }
    }
  });

  it("translates Round-2 voice preview, Breeze/Drama 3 TTS, symlink labels, and voiceGroupNeutral across all 6 locales", () => {
    const HAN_RE = /[\u3400-\u9fff]/;
    const FORBIDDEN_ZH_TW_RE =
      /工作流(?!程)|显存|顯存|尾帧|尾幀|渲染|视频|視頻|音频|音頻|默认|保存|缓存|緩存/;
    const STRICT_CANTONESE_RE = /[搵唔喺嘅啲咗冇佢咁]/;

    // 1. comfy.voiceGroupNeutral across all 6 locales
    expect(COLABONE_MESSAGES["zh-CN"]["comfy.voiceGroupNeutral"]).toBe("内置中性 / 通用音色");
    expect(COLABONE_MESSAGES["en-US"]["comfy.voiceGroupNeutral"]).toBe("Built-in Neutral Voices");
    expect(COLABONE_MESSAGES["zh-TW"]["comfy.voiceGroupNeutral"]).toBe("內建中性／通用音色");
    expect(COLABONE_MESSAGES["zh-HK"]["comfy.voiceGroupNeutral"]).toBe("內置中性／通用音色");
    expect(COLABONE_MESSAGES["ja-JP"]["comfy.voiceGroupNeutral"]).toBe("内蔵ニュートラル音声");
    expect(COLABONE_MESSAGES["ko-KR"]["comfy.voiceGroupNeutral"]).toBe("내장 중성 보이스");

    // 2. 14 exact strings, 2 dynamic patterns, and 3 symlink labels
    const r2Samples = [
      // 14 exact strings
      "当前远端服务版本暂不支持 TTS 音色试听，请先点击顶部「立即热更新」升级远端服务后再试",
      "复用已缓存的音色试听音频",
      "试听音频响应格式不合法",
      "试听音频体积超过 16 MiB 上限",
      "试听音频数据解码失败",
      "试听音频分片读取次数超出上限",
      "试听音频读取范围不合法",
      "试听音频分片偏移量必须为非负整数",
      "试听音频分片长度必须为正整数",
      "试听音频缓存已变更，请重试",
      "试听音频数据为空",
      "试听音频传输不完整，请重试",
      "试听音频数据校验失败，请重试",
      "Breeze TTS 2 需要 NVIDIA CUDA GPU 才能运行",
      // 2 dynamic patterns
      "复用已缓存的音色试听音频（时长 4.2 秒）",
      "Fish Audio Drama 3 至少需要 48 GB 显存（当前 24.0 GB），请切换至 G4 96 GB / A100 80 GB 运行时，或改用 MOSS-TTS v1.5 / VoxCPM2",
      // 3 symlink strings
      "TTS 就绪标记文件不能是符号链接",
      "音色参考音频不能是符号链接",
      "情绪参考音频不能是符号链接",
    ];

    const nonZhCnLocales: ColabOneLocale[] = [
      "en-US",
      "zh-TW",
      "zh-HK",
      "ja-JP",
      "ko-KR",
    ];

    for (const loc of nonZhCnLocales) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const sample of r2Samples) {
        const out = translateColabOneBackendText(sample, loc);
        expect(tb(sample), `tb() parity for ${loc}: ${sample}`).toBe(out);
        expect(out, `${loc} must translate "${sample}"`).not.toBe(sample);
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            HAN_RE.test(out),
            `Han character leak in ${loc} for "${sample}": "${out}"`,
          ).toBe(false);
        }
        if (loc === "zh-TW") {
          expect(
            out,
            `forbidden zh-TW term in "${sample}": "${out}"`,
          ).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(
            out,
            `spoken Cantonese leak in zh-HK for "${sample}": "${out}"`,
          ).not.toMatch(STRICT_CANTONESE_RE);
        }
      }
    }
  });

  it("localizes unified digital human studio card, engine switcher, and singular workflow count across all 6 locales", () => {
    const HAN_RE = /[\u3400-\u9fff]/;
    const FULLWIDTH_PUNCT_RE = /[（）「」，。：；“”《》【】、～]/;
    const FORBIDDEN_ZH_TW_RE =
      /工作流(?!程)|显存|顯存|尾帧|尾幀|渲染|视频|視頻|音频|音頻|默认|保存|缓存|緩存|全棧/;
    const STRICT_CANTONESE_RE = /[搵唔喺嘅啲咗冇佢咁]/;

    const unifiedKeys = [
      "comfy.studioWorkflowCountOne",
      "comfy.digitalHumanUnifiedName",
      "comfy.digitalHumanUnifiedDesc",
      "comfy.digitalHumanEngineCountSpec",
      "comfy.videoEngineLabel",
      "comfy.videoEngineGpuAutoHint",
      "comfy.videoEngineRecommendedTag",
      "comfy.videoEngineReadyTag",
      "comfy.videoEngineInfiniteTalkTitle",
      "comfy.videoEngineInfiniteTalkSummary",
      "comfy.videoEngineLongCatTitle",
      "comfy.videoEngineLongCatSummary",
    ] as const satisfies readonly ColabOneMessageKey[];

    // Singular vs plural studio workflow counts across locales
    expect(t("comfy.studioWorkflowCountOne", { count: 1 }, "en-US")).toBe(
      "1 workflow",
    );
    expect(t("comfy.studioWorkflowCount", { count: 2 }, "en-US")).toBe(
      "2 workflows",
    );
    expect(t("comfy.studioWorkflowCountOne", { count: 1 }, "zh-CN")).toBe(
      "1 个工作流",
    );
    expect(t("comfy.studioWorkflowCountOne", { count: 1 }, "zh-TW")).toBe(
      "1 個工作流程",
    );
    expect(t("comfy.studioWorkflowCountOne", { count: 1 }, "zh-HK")).toBe(
      "1 個工作流",
    );
    expect(t("comfy.studioWorkflowCountOne", { count: 1 }, "ja-JP")).toBe(
      "1 件のワークフロー",
    );
    expect(t("comfy.studioWorkflowCountOne", { count: 1 }, "ko-KR")).toBe(
      "워크플로 1개",
    );

    for (const loc of ALL_LOCALES) {
      const cat = COLABONE_MESSAGES[loc];
      for (const key of unifiedKeys) {
        const val = cat[key];
        expect(val.trim().length, `${loc}[${key}] must be non-empty`).toBeGreaterThan(
          0,
        );
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            HAN_RE.test(val),
            `Han leak in ${loc}[${key}]: "${val}"`,
          ).toBe(false);
          expect(
            FULLWIDTH_PUNCT_RE.test(val),
            `fullwidth punctuation leak in ${loc}[${key}]: "${val}"`,
          ).toBe(false);
        }
        if (loc === "zh-TW") {
          expect(
            val,
            `forbidden zh-TW term in ${key}: "${val}"`,
          ).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(
            val,
            `spoken Cantonese leak in zh-HK[${key}]: "${val}"`,
          ).not.toMatch(STRICT_CANTONESE_RE);
        }
      }

      // Placeholder interpolation checks
      const engineCountText = t(
        "comfy.digitalHumanEngineCountSpec",
        { count: 2 },
        loc,
      );
      expect(engineCountText, `${loc} engine count spec`).toContain("2");
      expect(engineCountText, `${loc} engine count unresolved`).not.toContain("{");

      const gpuAutoHintText = t(
        "comfy.videoEngineGpuAutoHint",
        { gpu: "G4 96G" },
        loc,
      );
      expect(gpuAutoHintText, `${loc} GPU auto hint`).toContain("G4 96G");
      expect(gpuAutoHintText, `${loc} GPU auto hint unresolved`).not.toContain("{");

      // Engine titles must not embed the dynamic "Recommended" badge text
      const recTag = cat["comfy.videoEngineRecommendedTag"];
      expect(
        cat["comfy.videoEngineInfiniteTalkTitle"].includes(recTag),
        `${loc} InfiniteTalk title must not duplicate recommended badge "${recTag}"`,
      ).toBe(false);
      expect(
        cat["comfy.videoEngineLongCatTitle"].includes(recTag),
        `${loc} LongCat title must not duplicate recommended badge "${recTag}"`,
      ).toBe(false);
    }
  });

  it("translates LongCat 1.6 96GB native catalog strings, non-commercial notice, quality presets, and Stage 1-4 progress messages across all 6 locales", () => {
    const HAN_RE = /[\u3400-\u9fff]/;
    const FULLWIDTH_PUNCT_RE = /[（）「」，。：；“”《》【】、～]/;
    const FORBIDDEN_ZH_TW_RE =
      /工作流(?!程)|显存|顯存|尾帧|尾幀|渲染|视频|視頻|音频|音頻|默认|保存|缓存|緩存|全棧/;
    const STRICT_CANTONESE_RE = /[搵唔喺嘅啲咗冇佢咁]/;
    const LEAKED_ZH_CN_IN_JA_RE =
      /正在准备|正在执行|正在清理|仅限非商用|画质与渲染|超分引擎|口播数字人|显存|远端|请先/;

    // 1. UI message keys: qualityPresetCinema/Balanced/Longform & updated videoEngineLongCatSummary
    const longCatNativeUiKeys = [
      "comfy.qualityPresetCinema",
      "comfy.qualityPresetBalanced",
      "comfy.qualityPresetLongform",
      "comfy.videoEngineLongCatSummary",
    ] as const satisfies readonly ColabOneMessageKey[];

    for (const key of longCatNativeUiKeys) {
      const seenValues = new Set<string>();
      for (const loc of ALL_LOCALES) {
        const val = COLABONE_MESSAGES[loc][key];
        expect(val.trim().length, `empty ${loc}[${key}]`).toBeGreaterThan(0);
        seenValues.add(val);
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            HAN_RE.test(val),
            `Han leak in ${loc}[${key}]: "${val}"`,
          ).toBe(false);
          expect(
            FULLWIDTH_PUNCT_RE.test(val),
            `fullwidth punctuation leak in ${loc}[${key}]: "${val}"`,
          ).toBe(false);
        }
        if (loc === "ja-JP") {
          expect(
            val,
            `Simplified Chinese leak in ja-JP[${key}]: "${val}"`,
          ).not.toMatch(LEAKED_ZH_CN_IN_JA_RE);
          expect(val, `ja-JP[${key}] must differ from zh-CN`).not.toBe(
            COLABONE_MESSAGES["zh-CN"][key],
          );
        }
        if (loc === "zh-TW") {
          expect(val, `forbidden zh-TW term in ${key}: "${val}"`).not.toMatch(
            FORBIDDEN_ZH_TW_RE,
          );
        }
        if (loc === "zh-HK") {
          expect(
            val,
            `spoken Cantonese leak in zh-HK[${key}]: "${val}"`,
          ).not.toMatch(STRICT_CANTONESE_RE);
        }
      }
      expect(
        seenValues.size,
        `${key} should have distinct localized values`,
      ).toBeGreaterThanOrEqual(4);
    }

    expect(COLABONE_MESSAGES["en-US"]["comfy.qualityPresetCinema"]).toBe(
      "Cinema Flagship",
    );
    expect(COLABONE_MESSAGES["en-US"]["comfy.qualityPresetBalanced"]).toBe(
      "Balanced HD",
    );
    expect(COLABONE_MESSAGES["en-US"]["comfy.qualityPresetLongform"]).toBe(
      "Long-Form Steady",
    );
    expect(COLABONE_MESSAGES["ja-JP"]["comfy.qualityPresetCinema"]).toBe(
      "シネマ・フラッグシップ",
    );
    expect(COLABONE_MESSAGES["ko-KR"]["comfy.qualityPresetCinema"]).toBe(
      "시네마 플래그십",
    );

    // 2. Collect every user-facing string directly from catalog/comfyui.json digital-human-longcat
    const longCatTemplate = comfyCatalog.templates.find(
      (tpl) => tpl.id === "digital-human-longcat",
    );
    expect(longCatTemplate).toBeTruthy();
    expect(longCatTemplate?.commercial_ok).toBe(false);
    expect(longCatTemplate?.pipeline?.video_backend).toBe(
      "longcat_avatar_native",
    );

    const longCatCatalogStrings: string[] = [];
    if (longCatTemplate) {
      for (const s of [
        longCatTemplate.name,
        longCatTemplate.description,
        longCatTemplate.license,
        longCatTemplate.license_notice,
      ]) {
        if (typeof s === "string" && s.trim()) {
          longCatCatalogStrings.push(s);
        }
      }
      for (const field of longCatTemplate.fields) {
        if (field.label?.trim()) longCatCatalogStrings.push(field.label);
        if (field.description?.trim()) {
          longCatCatalogStrings.push(field.description);
        }
        for (const optLabel of Object.values(field.option_labels || {})) {
          if (typeof optLabel === "string" && optLabel.trim()) {
            longCatCatalogStrings.push(optLabel);
          }
        }
      }
    }
    expect(longCatCatalogStrings.length).toBeGreaterThanOrEqual(60);

    // 3. Stage 1-4 backend worker progress, preflight errors, and symlink safety messages
    const stage1To4Messages = [
      // Stage 1: native runtime env, source checkout, venv, weights, vocal separation
      "正在准备远端原生 LongCat 1.6 环境与权重",
      "正在准备远端原生 LongCat 1.6 环境与权重（复用已检出的远端源码：LongCat-Video@abcdef1234）",
      "正在准备 LongCat-Video-Avatar 1.6 原生运行环境",
      "LongCat-Video-Avatar 1.6 原生环境与权重已就绪",
      "正在检出远端数字人引擎固定版本源码：abcdef1234",
      "复用已就绪的隔离环境：远端数字人引擎",
      "正在创建隔离虚拟环境：远端数字人引擎（保留宿主 PyTorch）",
      "正在安装远端数字人引擎运行依赖（共 12 个包，保留现有 PyTorch）",
      "远端数字人引擎隔离环境已就绪",
      "复用已缓存的模型权重：LongCat-Video 基础编码器与 VAE（meituan-longcat/LongCat-Video）",
      "正在下载模型权重：LongCat-Video 基础编码器与 VAE（meituan-longcat/LongCat-Video@abcdef1234）",
      "LongCat-Video 基础编码器与 VAE模型权重准备完成",
      "正在执行 Kim_Vocal_2 前置人声分离",
      // Stage 2: native 720p rendering
      "正在启动 LongCat-Video-Avatar 1.6 原生渲染进程",
      "正在执行原生 720p LongCat 1.6 渲染",
      "正在执行原生 720p LongCat 1.6 渲染（正在启动 LongCat-Video-Avatar 1.6 原生渲染进程）",
      "ComfyUI 工作流：进行中 · 40% · 口播数字人 · 96GB 电影级旗舰版 · 正在执行原生 720p LongCat 1.6 渲染",
      // Stage 3: VRAM release + SeedVR2 7B / FlashVSR+ diffusion super-resolution
      "正在准备远端全画幅超分环境与权重",
      "正在准备远端全画幅超分环境与权重（正在准备 SeedVR2 7B Sharp 扩散超分环境）",
      "SeedVR2 7B Sharp 扩散超分环境与权重已就绪",
      "正在清理上一阶段显存以加载全画幅超分模型",
      "正在启动远端扩散视频超分进程",
      "正在执行全画幅时空扩散超分",
      "正在执行全画幅时空扩散超分（正在启动远端扩散视频超分进程）",
      "ComfyUI 工作流：进行中 · 75% · 口播数字人 · 96GB 电影级旗舰版 · 正在执行全画幅时空扩散超分",
      // Stage 4: final cinema-grade muxing + cancellation
      "正在执行终稿电影级封装与 48 kHz 高保真音画合流",
      "ComfyUI 工作流：进行中 · 95% · 口播数字人 · 96GB 电影级旗舰版 · 正在执行终稿电影级封装与 48 kHz 高保真音画合流",
      "实例正在停止，已取消远端数字人任务",
      // Preflight VRAM/disk/engine errors & symlink safety labels
      "未检测到可用远端 GPU，LongCat-Video-Avatar 1.6 原生管线至少需要 48.0 GiB（49152 MiB）显存",
      "远端 GPU 显存不足：当前 NVIDIA L4 仅 24.0 GiB（24576 MiB），LongCat-Video-Avatar 1.6 原生管线至少需要 48.0 GiB（49152 MiB）显存",
      "远端工作区磁盘空间不足：权重已缓存，但生成与临时转码仍需至少 12.0 GiB（12288 MiB）可用空间，当前仅剩 4.5 GiB（4608 MiB）",
      "远端磁盘空间不足：LongCat-Video-Avatar 1.6 原生管线与超分模型至少需要 72.0 GiB（73728 MiB）可用空间，当前仅剩 20.0 GiB（20480 MiB）",
      "不支持的远端扩散超分引擎：legacy_2x（仅支持 seedvr2_7b_sharp、seedvr2_7b、flashvsr_plus）",
      "编译缓存目录不能是符号链接",
      "Triton 编译缓存目录不能是符号链接",
      "数字人引擎根目录不能是符号链接",
      "数字人权重目录不能是符号链接",
      "数字人源码目录不能是符号链接",
      "Stage 2 工作目录不能是符号链接",
      "Stage 3 超分工作目录不能是符号链接",
      // 4. Real emitted backend messages from server/digital_human.py (_run_longcat_native_pipeline & validators)
      "正在释放远端 ComfyUI 显存以独占 96GB 跑原生旗舰引擎",
      "停止远端 ComfyUI 时出现非致命提示：socket closed",
      "正在准备 LongCat-Video-Avatar 1.6 原生旗舰环境与权重：口播数字人 · 96GB 电影级旗舰版",
      "口播数字人 · 96GB 电影级旗舰版 · 正在使用 LongCat 1.6 原生引擎渲染 720p 主画面（1280×720，240 帧，non_distill_cfg）",
      "LongCat-Video-Avatar 1.6 渲染失败：未产出有效的 720p 无损中间视频",
      "口播数字人 · 96GB 电影级旗舰版 · LongCat 1.6 原生 720p 主画面生成完成（耗时 2 分 15 秒）",
      "正在准备视频扩散超分引擎与权重：seedvr2_7b_sharp",
      "口播数字人 · 96GB 电影级旗舰版 · 正在执行 seedvr2_7b_sharp 视频扩散超分（1920×1080）",
      "视频扩散超分失败（seedvr2_7b_sharp）：未产出有效的无损超分视频",
      "口播数字人 · 96GB 电影级旗舰版 · seedvr2_7b_sharp 视频扩散超分完成（耗时 1 分 05 秒）",
      "正在进行单次母版封装（CRF 16 · 1920×1080 · 48 kHz AAC 256k）",
      "口播数字人 · 96GB 电影级旗舰版 · 原生旗舰数字人生成完成（LongCat 1.6 + seedvr2_7b_sharp，15.0 秒，1920×1080，总耗时 3 分 40 秒）",
      "口播数字人 · 96GB 电影级旗舰版 · 原生旗舰数字人生成完成（LongCat 1.6，15.0 秒，1280×720，总耗时 2 分 10 秒）",
      "非蒸馏采样步数 参数格式无效：不能为布尔值",
      "非蒸馏采样步数 必须为有限数值",
      "非蒸馏采样步数 参数格式无效：'abc'",
      "音频引导强度 参数格式无效：不能为布尔值",
      "文本引导强度 必须为有限数值",
      "双 LoRA 前段步数 参数格式无效：不能为布尔值",
      "双 LoRA 混合权重 必须为有限数值",
      "身份参考帧索引 参数格式无效：不能为布尔值",
      "终稿编码质量 CRF 必须为有限数值",
      "终稿编码质量 CRF 参数格式无效：'bad'",
      "随机种子 参数格式无效：不能为布尔值",
      "Stage 2 视频路径不能包含换行符或空字符",
      "Stage 2 输出视频路径不能包含换行符或空字符",
      "Stage 3 超分视频路径不能包含换行符或空字符",
      "Stage 3 超分输出路径不能包含换行符或空字符",
      "最终输出视频路径不能包含换行符或空字符",
      "主音频路径不能包含换行符或空字符",
      "不支持的 PCM 采样位深：3 字节",
      // 5. Real emitted backend messages from server/avatar_engines.py (dynamic symlink paths, weights, git/checkout, timeouts, manifests, path containment)
      "不支持的远端扩散超分引擎：'legacy_2x'（仅支持 seedvr2_7b_sharp、seedvr2_7b、flashvsr_plus）",
      "不支持的远端扩散超分引擎：'legacy_2x'（仅支持 seedvr2_7b_sharp、seedvr2_7b、flashvsr_plus、none、auto）",
      "目标文件不能是符号链接：stage2.mkv",
      "数字人引擎根目录不能是符号链接：/content/avatar_engines",
      "数字人权重目录不能是符号链接：/content/avatar_engines/weights",
      "本地权重目录（longcat_video）不能是符号链接：/content/avatar_engines/weights/longcat_video",
      "Drive 缓存目录不能是符号链接：/content/drive/MyDrive/ColabOne_Drive/avatar_engines",
      "Drive 权重目录不能是符号链接：/content/drive/MyDrive/ColabOne_Drive/avatar_engines/weights",
      "Drive 权重目录（seedvr2_7b）不能是符号链接：/content/drive/MyDrive/ColabOne_Drive/avatar_engines/weights/seedvr2_7b",
      "编译缓存目录不能是符号链接：/content/avatar_engines/inductor_cache",
      "Triton 编译缓存目录不能是符号链接：/content/avatar_engines/triton_cache",
      "Stage 2 工作目录不能是符号链接：/tmp/work/s2",
      "Stage 2 配置文件不能是符号链接：/tmp/work/s2/config.json",
      "Stage 3 超分工作目录不能是符号链接：/tmp/work/s3",
      "Stage 3 超分配置文件不能是符号链接：/tmp/work/s3/config.json",
      "虚拟环境继承路径文件不能是符号链接：/content/venv/site.pth",
      "运行日志目录不能是符号链接：/content/logs",
      "运行日志文件不能是符号链接：/content/logs/run.log",
      "数字人源码目录不能是符号链接：/content/repos",
      "数字人运行日志目录不能是符号链接：/content/logs",
      "LongCat-Video 源码目录不能是符号链接：/content/repos/longcat",
      "LongCat-Video Git 元数据目录不能是符号链接：/content/repos/longcat/.git",
      "LongCat-Video 源码就绪标记不能是符号链接：/content/repos/longcat/.ready",
      "LongCat-Video 虚拟环境目录不能是符号链接：/content/venvs/longcat",
      "LongCat-Video 环境就绪标记不能是符号链接：/content/venvs/longcat/.ready",
      "LongCat-Video 权重目录不能是符号链接：/content/weights/longcat",
      "LongCat-Video 权重标记不能是符号链接：/content/weights/longcat/.ready",
      "LongCat-Video 日志文件不能是符号链接：/content/logs/longcat.log",
      "Stage 2 输出视频 路径不合法：'/etc/passwd'",
      "Stage 2 输出视频 路径不能包含符号链接：/tmp/work/link",
      "Stage 2 输出视频 必须位于工作目录内：/etc/passwd",
      "Stage 2 输出视频 不能指向工作目录本身：/tmp/work",
      "LongCat-Video 隔离虚拟环境缺少 Python 可执行文件，拒绝回退到宿主环境：/content/venvs/longcat",
      "无法检测远端磁盘可用空间（permission denied），LongCat-Video-Avatar 1.6 至少需要 72.0 GiB 可用磁盘空间",
      "远端工作区磁盘空间不足：权重已缓存或将写入 Drive，但生成与临时转码仍需至少 12.0 GiB（12288 MiB）可用空间，当前仅剩 4.5 GiB（4608 MiB）",
      "LongCat-Video 源码提交哈希必须为完整 40 位十六进制 SHA：'abc123'",
      "远端缺少 Git，无法拉取 LongCat-Video 源码",
      "LongCat-Video 源码检出校验失败，HEAD 与目标提交 abcdef1234 不一致或关键文件缺失",
      "LongCat-Video 现有 Git 仓库异常，正在清空并重新检出：corrupt index",
      "LongCat-Video 虚拟环境创建不完整，缺少 Python 可执行文件：/content/venvs/longcat",
      "可选依赖（audio-separator）安装失败，已跳过，LongCat-Video 将使用原始音频继续运行：network error",
      "缺少 huggingface_hub，无法下载模型权重",
      "下载 LongCat-Video 模型权重失败（meituan-longcat/LongCat-Video）：HTTP 502",
      "LongCat-Video 模型权重下载不完整，缺少必需权重文件：/content/weights/longcat",
      "LongCat-Video-Avatar 1.6 原生渲染 执行超时，请查看运行日志",
      "LongCat-Video-Avatar 1.6 原生渲染 长时间无输出与进度更新（超过 1800 秒），进程疑似卡死，请查看运行日志",
      "LongCat-Video-Avatar 1.6 原生渲染 进程退出超时，请查看运行日志",
      "LongCat-Video-Avatar 1.6 原生渲染 执行失败（退出码 1）：CUDA OOM",
      "LongCat-Video-Avatar 1.6 原生渲染 执行完成但未生成有效输出清单：/tmp/s2/manifest.json",
      "LongCat-Video-Avatar 1.6 原生渲染 输出清单解析失败：Expecting value",
      "LongCat-Video-Avatar 1.6 原生渲染 输出清单格式无效或为空",
      "LongCat-Video-Avatar 1.6 原生渲染 返回错误：missing tensor",
      "LongCat-Video-Avatar 1.6 原生渲染 声明的输出视频不存在或为空：/tmp/s2/out.mkv",
      "LongCat-Video-Avatar 1.6 原生渲染 输出清单缺少有效的输出视频路径（output_video_path）",
      // 6. All 11 standardized Chinese progress strings from server/avatar_worker_longcat16.py
      "正在初始化 LongCat-Video-Avatar 1.6（non_distill_cfg，共 6 段）",
      "正在初始化 LongCat-Video-Avatar 1.6（distill_lora_mix，共 3 段）",
      "正在准备音频特征（前置人声分离：开启）",
      "正在准备音频特征（前置人声分离：关闭）",
      "正在加载纯 BF16 LongCat-Video-Avatar 1.6 模型权重",
      "正在加载 BF16 Avatar 1.6 DiT 主模型",
      "正在配置 torch.compile 无损算子编译",
      "正在提取全局 Whisper-Large-v3 音频特征",
      "正在渲染第 1/6 段（首段 AI2V）",
      "已完成第 1/6 段（累计 93/512 帧）",
      "正在渲染第 2/6 段（13 帧连续续写）",
      "正在封装无损 FFV1 中间视频并写入输出清单",
      "LongCat-Video-Avatar 1.6 渲染完成（共 512 帧，耗时 84.3 秒）",
      // 7. All 7 standardized Chinese progress strings from server/vsr_worker_diffusion.py
      "正在启动 seedvr2_7b_sharp 超分（短边 1080px，批次 41，共 13 批）",
      "正在启动 flashvsr_plus 超分（短边 1080px，批次 32，共 16 批）",
      "正在执行 SeedVR2 时空扩散超分（seedvr2_7b_sharp）",
      "正在执行 SeedVR2 时空扩散超分（seedvr2_7b）",
      "正在将 SeedVR2 超分帧封装为无损 FFV1 中间视频",
      "正在加载 FlashVSR+ v1.1 流式扩散模型（1920×1080）",
      "正在执行 FlashVSR+ v1.1 流式扩散超分（第 3/12 段）",
      "正在将 FlashVSR+ 超分帧封装为无损 FFV1 中间视频",
      "已完成 seedvr2_7b_sharp 视频扩散超分（共 512 帧，耗时 42.5 秒）",
      "已完成 flashvsr_plus 视频扩散超分（共 512 帧，耗时 18.2 秒）",
    ];

    expect(longCatTemplate?.license).toBe("MIT / Apache-2.0");

    const allLongCatNativeSamples = [
      ...new Set([...longCatCatalogStrings, ...stage1To4Messages]),
    ];

    for (const loc of ALL_LOCALES) {
      setColabOneLocale(loc, { persist: false, syncNative: false });
      for (const sample of allLongCatNativeSamples) {
        const out = translateColabOneBackendText(sample, loc);
        const requiresTranslation = CJK_OR_FULLWIDTH_RE.test(sample);
        expect(tb(sample), `tb() parity for ${loc}: ${sample}`).toBe(out);
        expect(
          out.trim().length,
          `empty translation for ${loc}: ${sample}`,
        ).toBeGreaterThan(0);

        if (loc === "zh-CN") {
          expect(out).toBe(sample);
          continue;
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `CJK/fullwidth leak in ${loc} for "${sample}": "${out}"`,
          ).toBe(false);
          if (requiresTranslation) {
            expect(out, `${loc} must translate "${sample}"`).not.toBe(sample);
          } else {
            expect(out, `${loc} pure-ASCII pass-through for "${sample}"`).toBe(
              sample,
            );
          }
        }
        if (loc === "ja-JP") {
          if (requiresTranslation) {
            expect(out, `ja-JP must translate "${sample}"`).not.toBe(sample);
          } else {
            expect(out, `ja-JP pure-ASCII pass-through for "${sample}"`).toBe(
              sample,
            );
          }
          expect(
            out,
            `Simplified Chinese leak in ja-JP for "${sample}": "${out}"`,
          ).not.toMatch(LEAKED_ZH_CN_IN_JA_RE);
        }
        if (loc === "zh-TW") {
          expect(
            out,
            `forbidden zh-TW term for "${sample}": "${out}"`,
          ).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(
            out,
            `spoken Cantonese leak in zh-HK for "${sample}": "${out}"`,
          ).not.toMatch(STRICT_CANTONESE_RE);
        }
      }
    }

    // Spot-check key multi-locale translations & locale quality expectations
    expect(
      translateColabOneBackendText("口播数字人 · 96GB 电影级旗舰版", "en-US"),
    ).toBe("Talking Avatar · 96GB Cinema Flagship");
    expect(
      translateColabOneBackendText(
        "本旗舰数字人集成方案（LongCat-Video-Avatar 1.6 + SeedVR2 7B + FlashVSR+ 及内置旗舰 TTS）仅限个人学习、研究与非商用演示，严禁用于任何商业用途。",
        "en-US",
      ),
    ).toContain("strictly for personal study, research, and non-commercial");
    expect(
      translateColabOneBackendText(
        "正在执行原生 720p LongCat 1.6 渲染",
        "ja-JP",
      ),
    ).toBe("ネイティブ 720p LongCat 1.6 動画をレンダリング中");
    expect(
      translateColabOneBackendText(
        "正在执行全画幅时空扩散超分",
        "ko-KR",
      ),
    ).toBe("풀프레임 시공간 확산 업스케일 실행 중");
    expect(
      translateColabOneBackendText(
        "正在执行终稿电影级封装与 48 kHz 高保真音画合流",
        "en-US",
      ),
    ).toBe("Running final cinema-grade muxing with 48 kHz high-fidelity audio");

    // Locale quality assertions: ja-JP 2K labels and zh-TW terminology
    expect(
      translateColabOneBackendText("横屏超清 2K（16:9 · 极致细腻）", "ja-JP"),
    ).toContain("超高精細ディテール");
    expect(
      translateColabOneBackendText("竖屏超清 2K（9:16 · 极致细腻）", "ja-JP"),
    ).toContain("超高精細ディテール");
    expect(t("comfy.qualityPresetBalanced", undefined, "zh-TW")).toBe(
      "高畫質均衡",
    );
    expect(
      translateColabOneBackendText(
        "高质均衡（8 步 DMD+OPSD 双 LoRA + SeedVR2 7B · 约 4～6 分钟/分钟片）",
        "zh-TW",
      ),
    ).toContain("高畫質均衡");
    expect(
      translateColabOneBackendText(
        "8 步 DMD+OPSD 双 LoRA 混合蒸馏（高质高速兼顾）",
        "zh-TW",
      ),
    ).toContain("畫質與速度兼顧");

    // Verify the 7 additional real emitted server/digital_human.py native pipeline messages across all 6 locales
    const extraDigitalHumanMessages = [
      "正在停止远端 ComfyUI 以释放完整显存供配音与原生旗舰引擎使用",
      "正在预处理上传音频（48 kHz 无损直通，保留立体声与原始动态）",
      "终稿体积 612.4 MiB 超过 512 MiB 上限，正在按目标视频码率 18500 kbps 自动重编码一次以保留本次渲染成果",
      "远端磁盘空间不足：TTS 引擎 Qwen3-TTS 1.7B（约 8.0 GiB）与 LongCat-Video-Avatar 1.6 原生管线（约 72.0 GiB）合计至少需要 80.0 GiB（81920 MiB）可用空间，当前仅剩 40.5 GiB（41472 MiB）",
      "远端磁盘空间不足：LongCat-Video-Avatar 1.6 原生管线与所选采样/人声模式至少需要 72.0 GiB（73728 MiB）可用空间，当前仅剩 30.0 GiB（30720 MiB）",
      "配音完成后无法检测远端磁盘空间（OSError: disk offline），LongCat-Video-Avatar 1.6 至少需要 72.0 GiB 可用磁盘空间",
      "远端磁盘空间不足：配音完成后仅剩 28.5 GiB（29184 MiB），不足以继续运行 LongCat-Video-Avatar 1.6 原生管线（至少需要 72.0 GiB / 73728 MiB）",
    ];
    for (const sample of extraDigitalHumanMessages) {
      for (const loc of ALL_LOCALES) {
        const out = translateColabOneBackendText(sample, loc);
        expect(out.length, `${loc}: ${sample}`).toBeGreaterThan(0);
        if (loc !== "zh-CN") {
          expect(out, `${loc} must translate ${sample}`).not.toBe(sample);
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `${loc} leaked CJK/fullwidth in "${out}"`,
          ).toBe(false);
        }
      }
    }
  });

  it("translates all Round-2 backend, Go controller, worker-containment, symlink, updater/SOCKS5, zh-TW script, and IndexTTS-2.5 messages cleanly across all 6 locales", () => {
    const CJK_OR_FULLWIDTH_RE =
      /[\u3000-\u303F\u3040-\u30FF\u3400-\u4DBF\u4E00-\u9FFF\uF900-\uFAFF\uFF00-\uFFEF]/;
    const LEAKED_ZH_CN_IN_JA_RE =
      /正在|远端|显存|请先|失败|符号链接|不支持|校验|文件|目录|草稿/;
    const FORBIDDEN_ZH_TW_RE =
      /视频|音频|默认|渲染|显存|文件|脚本|缓存|符号链接|路径|校验/;
    const STRICT_CANTONESE_RE = /[咗喺嘅啲冇睇唔]/;

    const round2Messages = [
      // Audio preprocessing & 24000 Hz mono PCM16 WAV transcoding
      "音频预处理完成，总时长 12.5 秒",
      "正在将参考音频转码为标准 24000 Hz 单声道 PCM16 WAV：voice_ref.mp3",
      "参考音频转码失败，未生成输出文件：/tmp/out.wav",
      "参考音频转码失败，未生成有效的单声道 PCM16 WAV：/tmp/out.wav",
      // Remote TTS venv incomplete
      "远端 TTS 引擎虚拟环境创建不完整，缺少 Python 可执行文件：/content/venv",
      // Worker-file containment & segment safety errors
      "Stage 2 工作目录安全校验失败",
      "Stage 2 输出视频路径超出工作目录安全范围",
      "Stage 2 输出视频路径包含符号链接，安全校验失败",
      "Stage 2 输出视频路径安全校验失败",
      "输出文件路径不合法",
      "没有可拼接的视频分段",
      "分段输出文件路径无效或为符号链接",
      "分段输出文件路径安全校验失败",
      "save_pcm_wav 缺少输出路径或音频数据",
      // common/comfyui.py & backend validation errors
      "video_backend 为 longcat_avatar_native 时流水线类型必须为 digital_human",
      "visible_when 必须为对象",
      "step 必须大于 0",
      "选项数量不能超过 128 个",
      "选项标签数量不能超过 128 个",
      "hf_token 必须为字符串",
      "下拉字段 pipeline.mode 的默认值不在 options 列表中",
      "字段 pipeline.script_text 的文本长度不能超过 10000 个字符",
      "非法的 GitHub 仓库地址：http://evil.example/repo",
      "TTS 引擎源码仓库仅允许使用 https://github.com/<owner>/<repo> 地址：https://gitlab.com/a/b",
      "Ming-3.0-TTS 未生成参考锚点音频：anchor.wav",
      "加速包源文件不合法：bad.whl",
      "远端命令执行失败：exit status 1",
      "不支持的模型文件扩展名：.bin",
      "检测到生成阶段耗尽上下文上限（8192 tokens），已自动提高后续请求预留输出空间（+512 tokens）",
      // comfyui_longcat_native upgrade hint & Go rune draft cap error
      "当前远端实例服务版本较旧，暂不支持原生 LongCat-Video-Avatar 1.6 管线，请先点击「热更新服务」或重新启动 GPU 实例",
      "草稿文本字段超过 200000 个字符上限",
      // All 6 new SYMLINK_DIR_LABELS
      "加速包缓存文件不能是符号链接：/tmp/accel.whl",
      "加速包目标目录不能是符号链接：/tmp/accel_dir",
      "加速包目标文件不能是符号链接：/tmp/accel_target",
      "加速包 wheel 文件不能是符号链接：/tmp/sage.whl",
      "下载临时文件不能是符号链接：/tmp/model.part",
      "下载元数据文件不能是符号链接：/tmp/model.meta.json",
      // Updater & SOCKS5 proxy errors (exact & dynamic)
      "请求失败",
      "更新服务未初始化",
      "正在检查 ColabOne 客户端更新…",
      "SOCKS5 代理目标连接被拒绝",
      "连接 SOCKS5 代理超时",
      "连接或下载超时",
      "HTTPS/TLS 证书校验失败",
      "网络连接失败",
      "未找到当前平台的更新清单",
      "GitHub 访问频率受限",
      "更新补丁签名校验未通过（可能已被篡改或损坏）",
      "安装目录缺少写入权限",
      "ColabOne v0.2.30 更新已就绪，重启客户端后生效",
      "检查客户端更新失败（网络连接失败）：dial timeout",
      "后台检查客户端更新失败（GitHub 访问频率受限）：403",
      "检查客户端更新失败：unexpected EOF",
      "后台检查客户端更新失败：context canceled",
      "SOCKS5 代理连接失败：auth failed",
      "代理拒绝连接 (HTTP 407)",
      // IndexTTS-2.5 messages
      "IndexTTS-2.5（情绪分离 + 拼音纠音 · 仅非商用）",
      "情绪参考音频（IndexTTS-2.5 专属，可选）",
      "IndexTTS-2.5 推理失败：CUDA OOM",
      "IndexTTS-2.5 未返回第 1 段音频",
      "IndexTTS-2.5 未生成第 2 段音频：/tmp/seg2.wav",
    ];

    for (const sample of round2Messages) {
      for (const loc of ALL_LOCALES) {
        const out = translateColabOneBackendText(sample, loc);
        expect(out.length, `${loc}: ${sample}`).toBeGreaterThan(0);
        if (loc === "zh-CN") {
          expect(out).toBe(sample);
        } else {
          expect(out, `${loc} must translate "${sample}"`).not.toBe(sample);
        }
        if (loc === "en-US" || loc === "ko-KR") {
          expect(
            CJK_OR_FULLWIDTH_RE.test(out),
            `CJK/fullwidth leak in ${loc} for "${sample}": "${out}"`,
          ).toBe(false);
        }
        if (loc === "ja-JP") {
          expect(
            out,
            `Simplified Chinese leak in ja-JP for "${sample}": "${out}"`,
          ).not.toMatch(LEAKED_ZH_CN_IN_JA_RE);
        }
        if (loc === "zh-TW") {
          expect(
            out,
            `forbidden zh-TW term for "${sample}": "${out}"`,
          ).not.toMatch(FORBIDDEN_ZH_TW_RE);
        }
        if (loc === "zh-HK") {
          expect(
            out,
            `spoken Cantonese leak in zh-HK for "${sample}": "${out}"`,
          ).not.toMatch(STRICT_CANTONESE_RE);
        }
      }
    }

    // Spot-check zh-TW "指令碼" terminology for scripts
    expect(
      translateColabOneBackendText(
        "TTS 工作脚本路径不能是符号链接：/tmp/worker.py",
        "zh-TW",
      ),
    ).toContain("指令碼");
    expect(
      translateColabOneBackendText("不支持的 TTS 引擎脚本：bad_engine", "zh-TW"),
    ).toBe("不支援的 TTS 引擎指令碼：bad_engine");
    expect(
      translateColabOneBackendText(
        "文件上传完成，开始在远端内核执行引导脚本",
        "zh-TW",
      ),
    ).toContain("啟動指令碼");
    expect(
      translateColabOneBackendText(
        "更新包上传完成，开始在远端执行热更新脚本",
        "zh-TW",
      ),
    ).toContain("熱更新指令碼");

    // Spot-check IndexTTS-2.5 label preservation across all 6 locales
    for (const loc of ALL_LOCALES) {
      expect(
        translateColabOneBackendText(
          "IndexTTS-2.5（情绪分离 + 拼音纠音 · 仅非商用）",
          loc,
        ),
      ).toContain("IndexTTS-2.5");
      expect(
        translateColabOneBackendText("IndexTTS-2.5 推理失败：CUDA OOM", loc),
      ).toContain("IndexTTS-2.5");
    }

    // Spot-check workflow post-sync auto-release & offline Drive output messages across all 6 locales
    const autoReleaseSamples = [
      "已开启工作流完成后自动同步云盘并释放实例",
      "远端实例已完成作品同步至 Google 云盘并自动释放",
      "已取消自动同步释放，已保留当前实例",
      "已按设置取消自动释放，当前实例已保留",
      "输出作品已同步至 Google 云盘，正在重试释放实例…",
      "正在导出作品到 Google 云盘 (1/2) · demo.mp4",
      "作品导出到 Google 云盘失败（网络超时），已保留当前实例",
      "工作区同步到 Google 云盘失败（连接中断），已保留当前实例",
      "读取 Google 云盘目录失败（HTTP 403）",
      "读取 Google 云盘作品列表失败（HTTP 500）",
      "从 Google 云盘读取作品失败（HTTP 503）",
      "删除 Google 云盘作品失败（HTTP 500）",
    ];
    for (const loc of ALL_LOCALES) {
      for (const sample of autoReleaseSamples) {
        const out = translateColabOneBackendText(sample, loc);
        expect(out.length, `${loc}: ${sample}`).toBeGreaterThan(4);
        if (loc !== "zh-CN") {
          expect(out, `${loc}: ${sample}`).not.toBe(sample);
        }
        if (loc === "en-US" || loc === "ko-KR") {
          // Allow inner untranslated sample substrings only for custom error details
          if (!sample.includes("网络超时") && !sample.includes("连接中断")) {
            expect(CJK_OR_FULLWIDTH_RE.test(out), `${loc} leak: ${out}`).toBe(
              false,
            );
          }
        }
      }
    }
  });
});
