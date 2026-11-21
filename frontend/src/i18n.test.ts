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
      "已保存设置（默认 GPU: T4 · 空闲释放: 30 分钟 · 下载并发: 16 线程 · 已更新 Hugging Face Token）",
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
});
