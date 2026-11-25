import { afterEach, describe, expect, it, vi } from "vitest";
import {
  CLONE_ONLY_TTS_ENGINE_IDS,
  COLABONE_DEFAULT_VOICE_PREVIEW_TEXT,
  decodeColabOneBase64AudioUrl,
  hasNonCommercialNotice,
  isColabOneVoicePresetSupportedByEngine,
  isColabOneVoiceRefRequired,
  isColabOneVoiceRefTextRequired,
  missingRequiredWorkflowFields,
  resolveColabOneFallbackVoicePreset,
  resolveColabOneSelectedVoicePreset,
  resolveColabOneVoicePresetGroups,
  resolveColabOneVoicePreviewDefaultText,
  resolveNonCommercialNotice,
  verifyAndDecodeColabOneBase64AudioUrl,
} from "./comfyui";
import { t } from "./i18n";
import { translateColabOneBackendText } from "./i18n/backendMessages";
import type {
  ColabOneComfyTtsEngineSpec,
  ColabOneComfyTtsVoicePreset,
  WorkflowField,
  WorkflowTemplate,
} from "./types";

function makeVoicePreset(
  overrides: Partial<ColabOneComfyTtsVoicePreset> & { id: string; name: string },
): ColabOneComfyTtsVoicePreset {
  return {
    gender: "female",
    style: "知性主播",
    description: "测试音色描述",
    prompt: "clear warm studio microphone voice",
    preview_text: "你好，这是默认试听样本文本。",
    seed: 202601,
    ...overrides,
  };
}

function makeVoiceField(
  overrides: Partial<WorkflowField> = {},
): WorkflowField {
  return {
    key: "pipeline.voice_preset",
    label: "配语音色",
    node_id: "pipeline",
    input_name: "voice_preset",
    field_type: "select",
    default: "shen_zhiyi",
    options: ["shen_zhiyi", "chen_jingzhou", "qwen_vivian", "custom"],
    option_labels: {
      custom: "自定义音色（克隆或文本描述）",
    },
    ...overrides,
  };
}

function makeTtsEngine(
  overrides: Partial<ColabOneComfyTtsEngineSpec> & { id: string },
): ColabOneComfyTtsEngineSpec {
  return {
    name: overrides.id,
    description: "",
    repo: `org/${overrides.id}`,
    revision: "a".repeat(40),
    license: "Apache-2.0",
    commercial_ok: true,
    license_notice: "",
    min_vram_mib: 8192,
    min_disk_mib: 8192,
    sample_rate: 48000,
    max_segment_chars: 180,
    supports_voice_clone: true,
    supports_voice_design: false,
    supports_custom_speakers: false,
    supports_emotion_ref: false,
    supports_inline_tags: false,
    ...overrides,
  };
}

function makeWorkflow(
  overrides: Partial<WorkflowTemplate> = {},
): WorkflowTemplate {
  return {
    id: "digital-human-infinitetalk",
    name: "InfiniteTalk Digital Human",
    description: "Long-video digital human pipeline",
    source: "builtin",
    min_vram_mib: 40960,
    node_count: 16,
    fields: [],
    commercial_ok: true,
    license: "Apache-2.0",
    license_notice: "",
    pipeline: {
      kind: "digital_human",
      video_backend: "infinitetalk",
      default_tts_engine: "fish_drama3",
      supported_tts_engines: [
        "fish_drama3",
        "moss_tts_v1_5",
        "breeze_tts2",
        "ming_3_tts",
        "voxcpm2",
        "qwen3_tts",
        "indextts2",
        "fish_s2_pro",
      ],
    },
    ...overrides,
  };
}

describe("resolveColabOneVoicePresetGroups", () => {
  it("groups female, male, official Qwen3 CustomVoice speakers, and custom cleanly in canonical order", () => {
    const qwen3Engine = makeTtsEngine({
      id: "qwen3_tts",
      name: "Qwen3-TTS 1.7B",
      supports_custom_speakers: true,
      voice_presets: [
        makeVoicePreset({
          id: "qwen_vivian",
          name: "Vivian (Official)",
          gender: "female",
          speaker_id: "Vivian",
        }),
        makeVoicePreset({
          id: "chen_jingzhou",
          name: "沉稳男主讲 · 陈景舟",
          gender: "male",
        }),
        makeVoicePreset({
          id: "shen_zhiyi",
          name: "知性女主播 · 沈知意",
          gender: "female",
        }),
        makeVoicePreset({
          id: "qwen_ryan",
          name: "Ryan (Official)",
          gender: "male",
          speaker_id: "Ryan",
        }),
        makeVoicePreset({
          id: "lin_xiaoman",
          name: "甜美活力 · 林小满",
          gender: "female",
        }),
        makeVoicePreset({
          id: "lin_chenxu",
          name: "阳光青年 · 林晨煦",
          gender: "male",
        }),
      ],
    });

    const field = makeVoiceField({
      options: ["shen_zhiyi", "lin_xiaoman", "chen_jingzhou", "lin_chenxu", "custom"],
      option_labels: {
        shen_zhiyi: "知性女主播 · 沈知意（推荐）",
        custom: "自定义音色（上传参考音频）",
      },
    });

    const groups = resolveColabOneVoicePresetGroups(field, qwen3Engine);
    expect(groups.map((g) => g.key)).toEqual([
      "female",
      "male",
      "official",
      "custom",
    ]);

    expect(groups[0].options.map((o) => o.id)).toEqual([
      "shen_zhiyi",
      "lin_xiaoman",
    ]);
    expect(groups[0].options[0].label).toBe("知性女主播 · 沈知意（推荐）");
    expect(groups[1].options.map((o) => o.id)).toEqual([
      "chen_jingzhou",
      "lin_chenxu",
    ]);
    expect(groups[2].options.map((o) => o.id)).toEqual([
      "qwen_vivian",
      "qwen_ryan",
    ]);
    expect(groups[2].options.map((o) => o.preset?.speaker_id)).toEqual([
      "Vivian",
      "Ryan",
    ]);
    expect(groups[3].options.map((o) => o.id)).toEqual(["custom"]);
    expect(groups[3].options[0].label).toBe("自定义音色（上传参考音频）");
  });

  it("omits empty groups for engines without official speakers and deduplicates preset IDs", () => {
    const voxEngine = makeTtsEngine({
      id: "voxcpm2",
      voice_presets: [
        makeVoicePreset({ id: "shen_zhiyi", name: "沈知意", gender: "female" }),
        makeVoicePreset({ id: "shen_zhiyi", name: "重复条目", gender: "female" }),
        makeVoicePreset({ id: "zhou_moyan", name: "温柔书生 · 周墨言", gender: "male" }),
      ],
    });

    const groups = resolveColabOneVoicePresetGroups(
      makeVoiceField({ options: ["shen_zhiyi", "custom"] }),
      voxEngine,
    );
    expect(groups.map((g) => g.key)).toEqual(["female", "male", "custom"]);
    expect(groups[0].options).toHaveLength(1);
    expect(groups[1].options).toHaveLength(1);
    expect(groups[2].options.map((o) => o.id)).toEqual(["custom"]);
  });

  it("routes neutral-gender and unclassified non-custom built-in presets into the neutral group instead of custom", () => {
    const engine = makeTtsEngine({
      id: "qwen3_tts",
      voice_presets: [
        makeVoicePreset({ id: "shen_zhiyi", name: "沈知意", gender: "female" }),
        makeVoicePreset({ id: "chen_jingzhou", name: "陈景舟", gender: "male" }),
        makeVoicePreset({
          id: "neutral_narrator",
          name: "中性旁白",
          gender: "neutral",
        }),
        makeVoicePreset({
          id: "ambient_voice",
          name: "未标注性别预设",
          gender: "" as unknown as ColabOneComfyTtsVoicePreset["gender"],
        }),
        makeVoicePreset({
          id: "qwen_vivian",
          name: "Vivian",
          gender: "female",
          speaker_id: "Vivian",
        }),
      ],
    });

    const groups = resolveColabOneVoicePresetGroups(
      makeVoiceField({ options: ["shen_zhiyi", "neutral_narrator", "custom"] }),
      engine,
    );
    expect(groups.map((g) => g.key)).toEqual([
      "female",
      "male",
      "neutral",
      "official",
      "custom",
    ]);
    expect(groups[2].options.map((o) => o.id)).toEqual([
      "neutral_narrator",
      "ambient_voice",
    ]);
    expect(groups[4].options.map((o) => o.id)).toEqual(["custom"]);
  });

  it("falls back to field options when engine voice_presets are unavailable", () => {
    const field = makeVoiceField({
      options: ["shen_zhiyi", "qwen_sohee", "custom"],
      option_labels: {
        shen_zhiyi: "沈知意",
        qwen_sohee: "Sohee",
        custom: "自定义音色",
      },
    });
    const groups = resolveColabOneVoicePresetGroups(field, undefined);
    expect(groups.map((g) => g.key)).toEqual(["female", "official", "custom"]);
    expect(groups[0].options.map((o) => o.id)).toEqual(["shen_zhiyi"]);
    expect(groups[1].options.map((o) => o.id)).toEqual(["qwen_sohee"]);
    expect(groups[2].options.map((o) => o.id)).toEqual(["custom"]);
  });
});

describe("resolveColabOneSelectedVoicePreset and resolveColabOneVoicePreviewDefaultText", () => {
  const presets: ColabOneComfyTtsVoicePreset[] = [
    makeVoicePreset({
      id: "shen_zhiyi",
      name: "知性女主播 · 沈知意",
      preview_text: "  你好，我是沈知意，欢迎收看今天的节目。  ",
    }),
    makeVoicePreset({
      id: "blank_preview_voice",
      name: "无自定义试听文案",
      preview_text: "   ",
    }),
    makeVoicePreset({
      id: "qwen_vivian",
      name: "Vivian",
      speaker_id: "Vivian",
      preview_text: "你好，我是通义千问官方音色 Vivian。",
    }),
  ];
  const engine = makeTtsEngine({ id: "qwen3_tts", voice_presets: presets });

  it("resolves trimmed matching presets and returns undefined for custom, empty, or unknown IDs", () => {
    expect(resolveColabOneSelectedVoicePreset("shen_zhiyi", engine)?.id).toBe(
      "shen_zhiyi",
    );
    expect(
      resolveColabOneSelectedVoicePreset("  qwen_vivian  ", engine)?.speaker_id,
    ).toBe("Vivian");

    expect(resolveColabOneSelectedVoicePreset("custom", engine)).toBeUndefined();
    expect(resolveColabOneSelectedVoicePreset(" custom ", engine)).toBeUndefined();
    expect(resolveColabOneSelectedVoicePreset("", engine)).toBeUndefined();
    expect(resolveColabOneSelectedVoicePreset("   ", engine)).toBeUndefined();
    expect(resolveColabOneSelectedVoicePreset("unknown_id", engine)).toBeUndefined();
    expect(resolveColabOneSelectedVoicePreset("shen_zhiyi", undefined)).toBeUndefined();
  });

  it("returns trimmed preset preview_text or falls back to COLABONE_DEFAULT_VOICE_PREVIEW_TEXT", () => {
    const selected = resolveColabOneSelectedVoicePreset("shen_zhiyi", engine);
    expect(resolveColabOneVoicePreviewDefaultText(selected)).toBe(
      "你好，我是沈知意，欢迎收看今天的节目。",
    );

    const blankPreset = resolveColabOneSelectedVoicePreset(
      "blank_preview_voice",
      engine,
    );
    expect(resolveColabOneVoicePreviewDefaultText(blankPreset)).toBe(
      COLABONE_DEFAULT_VOICE_PREVIEW_TEXT,
    );
    expect(resolveColabOneVoicePreviewDefaultText(undefined)).toBe(
      COLABONE_DEFAULT_VOICE_PREVIEW_TEXT,
    );
    expect(
      resolveColabOneVoicePreviewDefaultText(
        makeVoicePreset({ id: "no_preview", name: "No Preview", preview_text: "" }),
      ),
    ).toBe(COLABONE_DEFAULT_VOICE_PREVIEW_TEXT);
  });

  it("falls back cleanly when switching away from an unsupported engine-specific preset", () => {
    const field = makeVoiceField({ default: "shen_zhiyi" });
    const voxOnly = makeTtsEngine({
      id: "voxcpm2",
      voice_presets: [
        makeVoicePreset({ id: "shen_zhiyi", name: "沈知意" }),
        makeVoicePreset({ id: "chen_jingzhou", name: "陈景舟", gender: "male" }),
      ],
    });
    expect(resolveColabOneFallbackVoicePreset(field, voxOnly)).toBe("shen_zhiyi");

    const customOnlyEngine = makeTtsEngine({
      id: "alt_tts",
      voice_presets: [makeVoicePreset({ id: "alt_voice_1", name: "Alt Voice 1" })],
    });
    expect(
      resolveColabOneFallbackVoicePreset(
        makeVoiceField({ default: "missing_voice" }),
        customOnlyEngine,
      ),
    ).toBe("alt_voice_1");
  });
});

describe("decodeColabOneBase64AudioUrl", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("creates an object URL from valid audio/wav base64 (including whitespace and codec params)", async () => {
    const fakeWavHeader = "RIFF\x24\x00\x00\x00WAVEfmt ";
    const b64 = btoa(fakeWavHeader);
    const paddedB64 = `${b64.slice(0, 6)}\n  ${b64.slice(6)}`;

    const createSpy = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:colabone-preview-wav");

    const url = decodeColabOneBase64AudioUrl(paddedB64, "audio/wav; codecs=1");
    expect(url).toBe("blob:colabone-preview-wav");
    expect(createSpy).toHaveBeenCalledTimes(1);

    const blob = createSpy.mock.calls[0][0] as Blob;
    expect(blob.type).toBe("audio/wav");
    expect(blob.size).toBe(fakeWavHeader.length);
    expect(await blob.text()).toBe(fakeWavHeader);
  });

  it("supports audio/mpeg and audio/flac and defaults to audio/wav when MIME is omitted", () => {
    const createSpy = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:colabone-audio-ok");

    expect(decodeColabOneBase64AudioUrl(btoa("wav-bytes"))).toBe(
      "blob:colabone-audio-ok",
    );
    expect(decodeColabOneBase64AudioUrl(btoa("mp3-bytes"), "audio/mpeg")).toBe(
      "blob:colabone-audio-ok",
    );
    expect(decodeColabOneBase64AudioUrl(btoa("flac-bytes"), "AUDIO/FLAC")).toBe(
      "blob:colabone-audio-ok",
    );
    expect(createSpy).toHaveBeenCalledTimes(3);
  });

  it("rejects non-audio MIME types and invalid/empty base64 payloads", () => {
    const createSpy = vi.spyOn(URL, "createObjectURL");

    for (const disallowedMime of [
      "text/html",
      "image/svg+xml",
      "image/png",
      "video/mp4",
      "application/javascript",
      "application/octet-stream",
      "audio/ogg",
    ]) {
      expect(() =>
        decodeColabOneBase64AudioUrl(btoa("payload"), disallowedMime),
      ).toThrow(t("comfy.errUnsupportedPreview"));
    }

    expect(() => decodeColabOneBase64AudioUrl("", "audio/wav")).toThrow(
      t("comfy.errInvalidOutputTransfer"),
    );
    expect(() => decodeColabOneBase64AudioUrl("   ", "audio/wav")).toThrow(
      t("comfy.errInvalidOutputTransfer"),
    );
    expect(() =>
      decodeColabOneBase64AudioUrl("!!!not-valid-base64!!!", "audio/wav"),
    ).toThrow(t("comfy.errInvalidOutputTransfer"));

    expect(createSpy).not.toHaveBeenCalled();
  });
});

describe("hasNonCommercialNotice and resolveNonCommercialNotice for all 8 flagship engines", () => {
  const commercialWf = makeWorkflow({ commercial_ok: true, license: "Apache-2.0" });

  it("flags fish_drama3, breeze_tts2, indextts2, and fish_s2_pro as non-commercial", () => {
    expect(resolveNonCommercialNotice).toBe(hasNonCommercialNotice);

    const nonCommercialSpecs: ColabOneComfyTtsEngineSpec[] = [
      makeTtsEngine({
        id: "fish_drama3",
        name: "Fish Audio Drama 3",
        license: "Fish Audio Community License v1.0",
        commercial_ok: false,
        license_notice:
          "仅限个人学习与非商业研究使用；严禁对外提供托管 TTS API 服务，商用须另行获取 Fish Audio 商业授权。",
      }),
      makeTtsEngine({
        id: "breeze_tts2",
        name: "Breeze TTS 2",
        license: "BreezeBlue Research & Non-Commercial License",
        commercial_ok: false,
        license_notice:
          "Breeze TTS 2 权重、衍生模型及自托管输出音频均仅限学术研究与非商业用途使用。",
      }),
      makeTtsEngine({
        id: "indextts2",
        name: "IndexTTS-2.5",
        license: "Bilibili Model Use License Agreement",
        commercial_ok: false,
        license_notice:
          "仅限个人学习与非商业研究使用；商用前须核查并获取 Bilibili / IndexTeam 上游商业授权。",
      }),
      makeTtsEngine({
        id: "fish_s2_pro",
        name: "Fish Speech S2-Pro",
        license: "Fish Audio Research License",
        commercial_ok: false,
        license_notice:
          "仅限个人学习与非商业研究使用；如需商业使用须另行向 Fish Audio 获取商业授权。",
      }),
    ];

    for (const spec of nonCommercialSpecs) {
      const notice = resolveNonCommercialNotice(commercialWf, spec);
      expect(notice).not.toBeNull();
      expect(notice?.restrictedBy).toBe("tts");
      expect(notice?.sourceName).toBe(spec.name);
      expect(notice?.license).toBe(spec.license);
      expect(notice?.notice).toBe(spec.license_notice);

      // Even if a legacy payload omits commercial_ok (undefined), known non-commercial engine IDs are still flagged.
      const legacySpec = {
        ...spec,
        commercial_ok: undefined,
      } as unknown as ColabOneComfyTtsEngineSpec;
      const legacyNotice = hasNonCommercialNotice(commercialWf, legacySpec);
      expect(legacyNotice).not.toBeNull();
      expect(legacyNotice?.restrictedBy).toBe("tts");
    }
  });

  it("returns null for commercial-OK engines (moss_tts_v1_5, ming_3_tts, voxcpm2, qwen3_tts)", () => {
    for (const id of ["moss_tts_v1_5", "ming_3_tts", "voxcpm2", "qwen3_tts"]) {
      const spec = makeTtsEngine({
        id,
        name: id,
        license: "Apache-2.0",
        commercial_ok: true,
        license_notice: "",
      });
      expect(hasNonCommercialNotice(commercialWf, spec)).toBeNull();
      expect(resolveNonCommercialNotice(commercialWf, spec)).toBeNull();
    }
  });
});

describe("backend i18n translations for TTS disk checks and Breeze validation", () => {
  it("translates check_disk errors and Breeze ref_text validation across non-Chinese locales", () => {
    const diskErr =
      "远端磁盘空间不足：TTS 引擎 Fish Audio Drama 3 至少需要 48.0 GiB（49152 MiB）可用空间，当前仅剩 12.5 GiB（12800 MiB）";
    const diskEn = translateColabOneBackendText(diskErr, "en-US");
    expect(diskEn).toContain("Insufficient remote disk space");
    expect(diskEn).toContain("48.0 GiB (49152 MiB)");
    expect(diskEn).toContain("12.5 GiB (12800 MiB)");

    const diskKo = translateColabOneBackendText(diskErr, "ko-KR");
    expect(diskKo).toContain("원격 디스크 공간 부족");
    expect(diskKo).not.toContain("引擎");

    const detectErr = "无法检测远端磁盘空间，Fish Audio Drama 3 需要至少 49152 MiB 可用磁盘空间";
    expect(translateColabOneBackendText(detectErr, "en-US")).toContain(
      "Unable to detect remote disk space",
    );

    const breezeErr = "Breeze TTS 2 克隆参考音色时必须同时填写「参考音色对应文字」";
    expect(translateColabOneBackendText(breezeErr, "en-US")).toContain("Breeze TTS 2");
    expect(translateColabOneBackendText(breezeErr, "en-US")).not.toBe(breezeErr);

    const newMessages = [
      "请求合成 TTS 音色试听：fish_drama3",
      "远端已受理 TTS 音色试听任务",
      "远端已受理 TTS 音色试听任务，但刷新状态失败（将自动重试）：timeout",
      "复用已缓存的音色试听样本：abcdef1234",
      "TTS 依赖清单文件不能是符号链接",
      "Fish Audio Drama 3 克隆参考音色时必须同时填写「参考音色对应文字」",
      "Ming-3.0-TTS 克隆参考音色时必须同时填写「参考音色对应文字」",
      "当前 TTS 引擎不支持该内置音色：qwen_vivian",
    ];
    for (const msg of newMessages) {
      const en = translateColabOneBackendText(msg, "en-US");
      const ko = translateColabOneBackendText(msg, "ko-KR");
      expect(en, `en-US untranslated for ${msg}`).not.toMatch(/[\u4e00-\u9fff]/);
      expect(ko, `ko-KR leaked Han for ${msg}`).not.toMatch(/[\u4e00-\u9fff]/);
    }
  });
});

describe("verifyAndDecodeColabOneBase64AudioUrl, male fallback grouping & voice_ref_text validation", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("verifies matching SHA-256 digest and rejects mismatched digests before creating Blob URL", async () => {
    const payloadText = "RIFF\x24\x00\x00\x00WAVEfmt verify-test";
    const bytes = new TextEncoder().encode(payloadText);
    const digestBuf = await crypto.subtle.digest("SHA-256", bytes);
    const expectedHex = Array.from(new Uint8Array(digestBuf))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const b64 = btoa(payloadText);

    const createSpy = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:colabone-verified-wav");

    const okUrl = await verifyAndDecodeColabOneBase64AudioUrl(
      b64,
      "audio/wav",
      expectedHex.toUpperCase(),
    );
    expect(okUrl).toBe("blob:colabone-verified-wav");
    expect(createSpy).toHaveBeenCalledTimes(1);

    await expect(
      verifyAndDecodeColabOneBase64AudioUrl(b64, "audio/wav", "0".repeat(64)),
    ).rejects.toThrow(t("comfy.errInvalidOutputTransfer"));
    expect(createSpy).toHaveBeenCalledTimes(1);
  });

  it("rejects malformed or non-string expectedSha256 and fails closed when crypto.subtle.digest is unavailable", async () => {
    const b64 = btoa("RIFF\x24\x00\x00\x00WAVEfmt strict-sha");
    const createSpy = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:colabone-strict-wav");

    for (const malformed of [
      "not-hex",
      "a".repeat(63),
      "a".repeat(65),
      "g".repeat(64),
    ]) {
      await expect(
        verifyAndDecodeColabOneBase64AudioUrl(b64, "audio/wav", malformed),
      ).rejects.toThrow(t("comfy.errInvalidOutputTransfer"));
    }

    for (const nonString of [12345, true, {}, []]) {
      await expect(
        verifyAndDecodeColabOneBase64AudioUrl(
          b64,
          "audio/wav",
          nonString as unknown as string,
        ),
      ).rejects.toThrow(t("comfy.errInvalidOutputTransfer"));
    }

    expect(createSpy).not.toHaveBeenCalled();

    // Fail closed when expectedSha256 is valid 64-char hex but crypto.subtle.digest is missing
    vi.stubGlobal("crypto", {});
    await expect(
      verifyAndDecodeColabOneBase64AudioUrl(b64, "audio/wav", "a".repeat(64)),
    ).rejects.toThrow(t("comfy.errOutputChecksum"));
    expect(createSpy).not.toHaveBeenCalled();

    // Malformed expectedSha256 is still rejected with errInvalidOutputTransfer before checking crypto.subtle.digest
    await expect(
      verifyAndDecodeColabOneBase64AudioUrl(b64, "audio/wav", "short"),
    ).rejects.toThrow(t("comfy.errInvalidOutputTransfer"));
    expect(createSpy).not.toHaveBeenCalled();

    // Omitting expectedSha256 still succeeds even without crypto.subtle.digest
    expect(await verifyAndDecodeColabOneBase64AudioUrl(b64, "audio/wav")).toBe(
      "blob:colabone-strict-wav",
    );
    expect(createSpy).toHaveBeenCalledTimes(1);
  });

  it("enforces voice_ref_audio requirement for all 4 clone-only engines in custom mode only, and never for voice-design engines", () => {
    expect(Array.from(CLONE_ONLY_TTS_ENGINE_IDS).sort()).toEqual([
      "fish_drama3",
      "fish_s2_pro",
      "indextts2",
      "moss_tts_v1_5",
    ]);

    const wfWithPresets = makeWorkflow({
      fields: [
        {
          key: "pipeline.audio_source",
          label: "配音方式",
          node_id: "pipeline",
          input_name: "audio_source",
          field_type: "select",
          default: "tts",
          options: ["tts", "audio"],
        },
        {
          key: "pipeline.tts_engine",
          label: "TTS 引擎",
          node_id: "pipeline",
          input_name: "tts_engine",
          field_type: "select",
          default: "fish_drama3",
        },
        makeVoiceField({
          options: ["shen_zhiyi", "chen_jingzhou", "custom"],
          visible_when: { "pipeline.audio_source": "tts" },
        }),
        {
          key: "pipeline.voice_ref_audio",
          label: "参考音频",
          node_id: "pipeline",
          input_name: "voice_ref_audio",
          field_type: "audio",
          required: false,
          visible_when: { "pipeline.audio_source": "tts" },
        },
      ],
    });

    for (const engineId of [
      "fish_drama3",
      "moss_tts_v1_5",
      "fish_s2_pro",
      "indextts2",
    ]) {
      const customVals = {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": engineId,
        "pipeline.voice_preset": "custom",
      };
      expect(isColabOneVoiceRefRequired(wfWithPresets, customVals)).toBe(true);
      expect(
        missingRequiredWorkflowFields(wfWithPresets, customVals, {}, {}).map(
          (f) => f.key,
        ),
      ).toEqual(["pipeline.voice_ref_audio"]);
      expect(
        missingRequiredWorkflowFields(
          wfWithPresets,
          customVals,
          { "pipeline.voice_ref_audio": "a".repeat(32) },
          {},
        ),
      ).toEqual([]);

      for (const builtInPreset of ["shen_zhiyi", "chen_jingzhou"]) {
        const presetVals = {
          "pipeline.audio_source": "tts",
          "pipeline.tts_engine": engineId,
          "pipeline.voice_preset": builtInPreset,
        };
        expect(isColabOneVoiceRefRequired(wfWithPresets, presetVals)).toBe(
          false,
        );
        expect(
          missingRequiredWorkflowFields(wfWithPresets, presetVals, {}, {}),
        ).toEqual([]);
      }
    }

    for (const engineId of [
      "voxcpm2",
      "qwen3_tts",
      "breeze_tts2",
      "ming_3_tts",
    ]) {
      for (const presetId of ["custom", "shen_zhiyi"]) {
        const vals = {
          "pipeline.audio_source": "tts",
          "pipeline.tts_engine": engineId,
          "pipeline.voice_preset": presetId,
        };
        expect(isColabOneVoiceRefRequired(wfWithPresets, vals)).toBe(false);
        expect(
          missingRequiredWorkflowFields(wfWithPresets, vals, {}, {}),
        ).toEqual([]);
      }
    }

    // Legacy workflow without pipeline.voice_preset preserves indextts2-only requirement
    const legacyWf = makeWorkflow({
      fields: wfWithPresets.fields.filter(
        (f) => f.key !== "pipeline.voice_preset",
      ),
    });
    expect(
      isColabOneVoiceRefRequired(legacyWf, {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": "indextts2",
      }),
    ).toBe(true);
    for (const engineId of ["fish_drama3", "moss_tts_v1_5", "fish_s2_pro", "voxcpm2"]) {
      expect(
        isColabOneVoiceRefRequired(legacyWf, {
          "pipeline.audio_source": "tts",
          "pipeline.tts_engine": engineId,
        }),
      ).toBe(false);
    }
  });

  it("classifies built-in male voice IDs under male group when engine metadata is unavailable", () => {
    const field = makeVoiceField({
      options: [
        "shen_zhiyi",
        "chen_jingzhou",
        "lin_chenxu",
        "zhou_moyan",
        "gu_zhengyuan",
        "su_hechuan",
        "qwen_vivian",
        "custom",
      ],
    });
    const groups = resolveColabOneVoicePresetGroups(field, undefined);
    expect(groups.map((g) => g.key)).toEqual(["female", "male", "official", "custom"]);
    expect(groups[0].options.map((o) => o.id)).toEqual(["shen_zhiyi"]);
    expect(groups[1].options.map((o) => o.id)).toEqual([
      "chen_jingzhou",
      "lin_chenxu",
      "zhou_moyan",
      "gu_zhengyuan",
      "su_hechuan",
    ]);
    expect(groups[2].options.map((o) => o.id)).toEqual(["qwen_vivian"]);
  });

  it("enforces voice_ref_text requirement on breeze_tts2, fish_drama3, and ming_3_tts (without emotion_prompt)", () => {
    const wf = makeWorkflow({
      fields: [
        {
          key: "pipeline.audio_source",
          label: "配音方式",
          node_id: "pipeline",
          input_name: "audio_source",
          field_type: "select",
          default: "tts",
          options: ["tts", "audio"],
        },
        {
          key: "pipeline.tts_engine",
          label: "TTS 引擎",
          node_id: "pipeline",
          input_name: "tts_engine",
          field_type: "select",
          default: "fish_drama3",
        },
        {
          key: "pipeline.voice_ref_audio",
          label: "参考音频",
          node_id: "pipeline",
          input_name: "voice_ref_audio",
          field_type: "audio",
        },
        {
          key: "pipeline.voice_ref_text",
          label: "参考音色对应文字（可选）",
          node_id: "pipeline",
          input_name: "voice_ref_text",
          field_type: "textarea",
          required: false,
          group: "advanced",
        },
        {
          key: "pipeline.emotion_prompt",
          label: "情绪提示",
          node_id: "pipeline",
          input_name: "emotion_prompt",
          field_type: "text",
        },
      ],
    });

    const uploaded = { "pipeline.voice_ref_audio": "a".repeat(32) };
    for (const eid of ["breeze_tts2", "fish_drama3", "ming_3_tts"]) {
      const vals = {
        "pipeline.audio_source": "tts",
        "pipeline.tts_engine": eid,
        "pipeline.voice_ref_text": "",
        "pipeline.emotion_prompt": "",
      };
      expect(isColabOneVoiceRefTextRequired(wf, vals, uploaded, {})).toBe(true);
      const missing = missingRequiredWorkflowFields(wf, vals, uploaded, {});
      expect(missing.map((f) => f.key)).toContain("pipeline.voice_ref_text");
    }

    // Ming-3.0-TTS with emotion_prompt does NOT require ref_text (uses speaker_only conditioning).
    expect(
      isColabOneVoiceRefTextRequired(
        wf,
        {
          "pipeline.audio_source": "tts",
          "pipeline.tts_engine": "ming_3_tts",
          "pipeline.voice_ref_text": "",
          "pipeline.emotion_prompt": "高兴",
        },
        uploaded,
        {},
      ),
    ).toBe(false);

    // Preset support check rejects Qwen-only speaker on non-Qwen engine.
    const dramaEngine = makeTtsEngine({
      id: "fish_drama3",
      voice_presets: [makeVoicePreset({ id: "shen_zhiyi", name: "沈知意" })],
    });
    expect(
      isColabOneVoicePresetSupportedByEngine("qwen_vivian", makeVoiceField(), dramaEngine),
    ).toBe(false);
    expect(
      isColabOneVoicePresetSupportedByEngine("shen_zhiyi", makeVoiceField(), dramaEngine),
    ).toBe(true);
  });
});
