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
      "模型 API ID 必须为字符串",
      "模型 API ID 不能为空",
      "colabone/auto 为系统保留别名，请使用其他模型 ID",
      "模型 API ID 仅支持字母、数字、点、下划线、连字符和斜杠，且不能以斜杠或点结尾",
      "找不到指定模型",
      "该模型 ID 已被其他模型使用，请换一个名称",
      "请先更新远端服务后再自定义模型 ID",
      "已更新模型 API ID：colabone/qwen3.8-flash-next",
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
  });
});
