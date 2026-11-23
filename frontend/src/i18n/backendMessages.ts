import type { ColabOneLocale } from "./flags";
import {
  EXACT_MESSAGES,
  REDACTION_TOKENS,
  SOURCE_LABELS,
  type ColabOneLocaleTable,
} from "./backendExactMessages";
import { translateColabOnePlaceName } from "./locations";

interface PatternRule {
  re: RegExp;
  when?: (text: string) => boolean;
  format: (m: RegExpMatchArray, locale: ColabOneLocale) => string;
}

const TASK_KIND_LABELS: Record<string, ColabOneLocaleTable> = {
  模型下载: {
    "zh-CN": "模型下载",
    "en-US": "Model download",
    "zh-TW": "模型下載",
    "zh-HK": "模型下載",
    "ja-JP": "モデルダウンロード",
    "ko-KR": "모델 다운로드",
  },
  模型加载: {
    "zh-CN": "模型加载",
    "en-US": "Model load",
    "zh-TW": "模型載入",
    "zh-HK": "模型載入",
    "ja-JP": "モデルロード",
    "ko-KR": "모델 로드",
  },
  "外部 API 对话": {
    "zh-CN": "外部 API 对话",
    "en-US": "Public API chat",
    "zh-TW": "外部 API 對話",
    "zh-HK": "外部 API 聊天",
    "ja-JP": "外部 API チャット",
    "ko-KR": "외부 API 채팅",
  },
  "外部 API Responses": {
    "zh-CN": "外部 API Responses",
    "en-US": "Public API Responses",
    "zh-TW": "外部 API Responses",
    "zh-HK": "外部 API Responses",
    "ja-JP": "外部 API Responses",
    "ko-KR": "외부 API Responses",
  },
  "外部 API 上下文压缩": {
    "zh-CN": "外部 API 上下文压缩",
    "en-US": "Public API context compaction",
    "zh-TW": "外部 API 上下文壓縮",
    "zh-HK": "外部 API 上下文壓縮",
    "ja-JP": "外部 API コンテキスト圧縮",
    "ko-KR": "외부 API 컨텍스트 압축",
  },
  "ComfyUI 启动": {
    "zh-CN": "ComfyUI 启动",
    "en-US": "ComfyUI startup",
    "zh-TW": "ComfyUI 啟動",
    "zh-HK": "ComfyUI 啟動",
    "ja-JP": "ComfyUI 起動",
    "ko-KR": "ComfyUI 시작",
  },
  "ComfyUI 工作流": {
    "zh-CN": "ComfyUI 工作流",
    "en-US": "ComfyUI workflow",
    "zh-TW": "ComfyUI 工作流程",
    "zh-HK": "ComfyUI 工作流",
    "ja-JP": "ComfyUI ワークフロー",
    "ko-KR": "ComfyUI 워크플로",
  },
  "ComfyUI 任务": {
    "zh-CN": "ComfyUI 任务",
    "en-US": "ComfyUI task",
    "zh-TW": "ComfyUI 任務",
    "zh-HK": "ComfyUI 任務",
    "ja-JP": "ComfyUI タスク",
    "ko-KR": "ComfyUI 작업",
  },
  远端任务: {
    "zh-CN": "远端任务",
    "en-US": "Remote task",
    "zh-TW": "遠端任務",
    "zh-HK": "遠端任務",
    "ja-JP": "リモートタスク",
    "ko-KR": "원격 작업",
  },
};

const TASK_STATE_LABELS: Record<string, ColabOneLocaleTable> = {
  进行中: {
    "zh-CN": "进行中",
    "en-US": "Running",
    "zh-TW": "進行中",
    "zh-HK": "進行中",
    "ja-JP": "実行中",
    "ko-KR": "진행 중",
  },
  已完成: {
    "zh-CN": "已完成",
    "en-US": "Completed",
    "zh-TW": "已完成",
    "zh-HK": "已完成",
    "ja-JP": "完了",
    "ko-KR": "완료됨",
  },
  失败: {
    "zh-CN": "失败",
    "en-US": "Failed",
    "zh-TW": "失敗",
    "zh-HK": "失敗",
    "ja-JP": "失敗",
    "ko-KR": "실패",
  },
  已取消: {
    "zh-CN": "已取消",
    "en-US": "Cancelled",
    "zh-TW": "已取消",
    "zh-HK": "已取消",
    "ja-JP": "キャンセル済み",
    "ko-KR": "취소됨",
  },
};

const SYMLINK_DIR_LABELS: Record<string, ColabOneLocaleTable> = {
  工作流目录: {
    "zh-CN": "工作流目录",
    "en-US": "Workflow directory",
    "zh-TW": "工作流程目錄",
    "zh-HK": "工作流目錄",
    "ja-JP": "ワークフローディレクトリ",
    "ko-KR": "워크플로 디렉터리",
  },
  作品目录: {
    "zh-CN": "作品目录",
    "en-US": "Output directory",
    "zh-TW": "作品目錄",
    "zh-HK": "作品目錄",
    "ja-JP": "出力ディレクトリ",
    "ko-KR": "결과물 디렉터리",
  },
  输入文件目录: {
    "zh-CN": "输入文件目录",
    "en-US": "Input file directory",
    "zh-TW": "輸入檔案目錄",
    "zh-HK": "輸入檔案目錄",
    "ja-JP": "入力ファイルディレクトリ",
    "ko-KR": "입력 파일 디렉터리",
  },
  回收目录: {
    "zh-CN": "回收目录",
    "en-US": "Trash directory",
    "zh-TW": "回收目錄",
    "zh-HK": "回收目錄",
    "ja-JP": "ゴミ箱ディレクトリ",
    "ko-KR": "휴지통 디렉터리",
  },
  "ComfyUI 模型目录": {
    "zh-CN": "ComfyUI 模型目录",
    "en-US": "ComfyUI models directory",
    "zh-TW": "ComfyUI 模型目錄",
    "zh-HK": "ComfyUI 模型目錄",
    "ja-JP": "ComfyUI モデルディレクトリ",
    "ko-KR": "ComfyUI 모델 디렉터리",
  },
  共享模型目录: {
    "zh-CN": "共享模型目录",
    "en-US": "Shared models directory",
    "zh-TW": "共用模型目錄",
    "zh-HK": "共享模型目錄",
    "ja-JP": "共有モデルディレクトリ",
    "ko-KR": "공유 모델 디렉터리",
  },
  "ComfyUI 输入目录": {
    "zh-CN": "ComfyUI 输入目录",
    "en-US": "ComfyUI input directory",
    "zh-TW": "ComfyUI 輸入目錄",
    "zh-HK": "ComfyUI 輸入目錄",
    "ja-JP": "ComfyUI 入力ディレクトリ",
    "ko-KR": "ComfyUI 입력 디렉터리",
  },
  "ComfyUI 输出目录": {
    "zh-CN": "ComfyUI 输出目录",
    "en-US": "ComfyUI output directory",
    "zh-TW": "ComfyUI 輸出目錄",
    "zh-HK": "ComfyUI 輸出目錄",
    "ja-JP": "ComfyUI 出力ディレクトリ",
    "ko-KR": "ComfyUI 출력 디렉터리",
  },
};

const COMFY_FIELD_SUFFIX_LABELS: Record<string, ColabOneLocaleTable> = {
  反向提示词: {
    "zh-CN": "反向提示词",
    "en-US": "Negative Prompt",
    "zh-TW": "反向提示詞",
    "zh-HK": "反向提示詞",
    "ja-JP": "ネガティブプロンプト",
    "ko-KR": "부정 프롬프트",
  },
  正向提示词: {
    "zh-CN": "正向提示词",
    "en-US": "Positive Prompt",
    "zh-TW": "正向提示詞",
    "zh-HK": "正向提示詞",
    "ja-JP": "ポジティブプロンプト",
    "ko-KR": "긍정 프롬프트",
  },
  文本输入: {
    "zh-CN": "文本输入",
    "en-US": "Text Input",
    "zh-TW": "文字輸入",
    "zh-HK": "文字輸入",
    "ja-JP": "テキスト入力",
    "ko-KR": "텍스트 입력",
  },
  参考图片: {
    "zh-CN": "参考图片",
    "en-US": "Reference Image",
    "zh-TW": "參考圖片",
    "zh-HK": "參考圖片",
    "ja-JP": "参照画像",
    "ko-KR": "참조 이미지",
  },
  输入音频: {
    "zh-CN": "输入音频",
    "en-US": "Input Audio",
    "zh-TW": "輸入音訊",
    "zh-HK": "輸入音訊",
    "ja-JP": "入力音声",
    "ko-KR": "입력 오디오",
  },
  宽度: {
    "zh-CN": "宽度",
    "en-US": "Width",
    "zh-TW": "寬度",
    "zh-HK": "寬度",
    "ja-JP": "幅",
    "ko-KR": "너비",
  },
  高度: {
    "zh-CN": "高度",
    "en-US": "Height",
    "zh-TW": "高度",
    "zh-HK": "高度",
    "ja-JP": "高さ",
    "ko-KR": "높이",
  },
  帧数: {
    "zh-CN": "帧数",
    "en-US": "Frames",
    "zh-TW": "影格數",
    "zh-HK": "幀數",
    "ja-JP": "フレーム数",
    "ko-KR": "프레임 수",
  },
  "帧率 (FPS)": {
    "zh-CN": "帧率 (FPS)",
    "en-US": "Frame Rate (FPS)",
    "zh-TW": "影格率 (FPS)",
    "zh-HK": "幀率 (FPS)",
    "ja-JP": "フレームレート (FPS)",
    "ko-KR": "프레임 속도 (FPS)",
  },
  批次大小: {
    "zh-CN": "批次大小",
    "en-US": "Batch Size",
    "zh-TW": "批次大小",
    "zh-HK": "批次大小",
    "ja-JP": "バッチサイズ",
    "ko-KR": "배치 크기",
  },
  采样步数: {
    "zh-CN": "采样步数",
    "en-US": "Sampling Steps",
    "zh-TW": "取樣步數",
    "zh-HK": "採樣步數",
    "ja-JP": "サンプリングステップ数",
    "ko-KR": "샘플링 스텝",
  },
  "CFG 引导系数": {
    "zh-CN": "CFG 引导系数",
    "en-US": "CFG Scale",
    "zh-TW": "CFG 引導係數",
    "zh-HK": "CFG 引導係數",
    "ja-JP": "CFG スケール",
    "ko-KR": "CFG 스케일",
  },
  重绘幅度: {
    "zh-CN": "重绘幅度",
    "en-US": "Denoise Strength",
    "zh-TW": "重繪幅度",
    "zh-HK": "重繪幅度",
    "ja-JP": "ノイズ除去強度",
    "ko-KR": "디노이즈 강도",
  },
  随机种子: {
    "zh-CN": "随机种子",
    "en-US": "Random Seed",
    "zh-TW": "隨機種子",
    "zh-HK": "隨機種子",
    "ja-JP": "ランダムシード",
    "ko-KR": "랜덤 시드",
  },
};

function translateSymlinkDirLabel(raw: string, loc: ColabOneLocale): string {
  const s = raw.trim();
  if (SYMLINK_DIR_LABELS[s]) return SYMLINK_DIR_LABELS[s][loc];
  const sub = s.match(/^ComfyUI 模型子目录 (.+)$/);
  if (sub) {
    const dir = sub[1];
    switch (loc) {
      case "en-US":
        return `ComfyUI model subdirectory ${dir}`;
      case "zh-TW":
        return `ComfyUI 模型子目錄 ${dir}`;
      case "zh-HK":
        return `ComfyUI 模型子目錄 ${dir}`;
      case "ja-JP":
        return `ComfyUI モデルサブディレクトリ ${dir}`;
      case "ko-KR":
        return `ComfyUI 모델 하위 디렉터리 ${dir}`;
      default:
        return s;
    }
  }
  return s;
}

function translateLoadPlanLabel(raw: string, loc: ColabOneLocale): string {
  const s = raw.trim();
  if (loc === "zh-CN") return s;
  if (s === "兼容模式装载") {
    return {
      "zh-CN": s,
      "en-US": "Compatibility mode",
      "zh-TW": "相容模式載入",
      "zh-HK": "兼容模式載入",
      "ja-JP": "互換モードロード",
      "ko-KR": "호환 모드 로드",
    }[loc];
  }
  const moeMatch = s.match(
    /^GPU \+ 内存 MoE 混合装配（--fit on · (\d+) 层专家内存卸载 · KV q8_0）$/,
  );
  if (moeMatch) {
    const layers = moeMatch[1];
    return {
      "zh-CN": s,
      "en-US": `GPU + RAM MoE hybrid (--fit on · ${layers} expert layers to RAM · KV q8_0)`,
      "zh-TW": `GPU + 記憶體 MoE 混合裝配（--fit on · ${layers} 層專家記憶體卸載 · KV q8_0）`,
      "zh-HK": `GPU + 記憶體 MoE 混合裝配（--fit on · ${layers} 層專家記憶體卸載 · KV q8_0）`,
      "ja-JP": `GPU + RAM MoE ハイブリッド（--fit on · ${layers} 層エキスパート RAM オフロード · KV q8_0）`,
      "ko-KR": `GPU + RAM MoE 하이브리드 (--fit on · ${layers}개 전문가 계층 RAM 오프로드 · KV q8_0)`,
    }[loc];
  }
  const manualMoeMatch = s.match(
    /^GPU \+ 内存 MoE 手动卸载回退（--fit off · (\d+) 层专家内存卸载）$/,
  );
  if (manualMoeMatch) {
    const layers = manualMoeMatch[1];
    return {
      "zh-CN": s,
      "en-US": `GPU + RAM manual MoE fallback (--fit off · ${layers} expert layers to RAM)`,
      "zh-TW": `GPU + 記憶體 MoE 手動卸載回退（--fit off · ${layers} 層專家記憶體卸載）`,
      "zh-HK": `GPU + 記憶體 MoE 手動卸載回退（--fit off · ${layers} 層專家記憶體卸載）`,
      "ja-JP": `GPU + RAM 手動 MoE フォールバック（--fit off · ${layers} 層エキスパート RAM オフロード）`,
      "ko-KR": `GPU + RAM 수동 MoE 폴백 (--fit off · ${layers}개 전문가 계층 RAM 오프로드)`,
    }[loc];
  }
  const exactPlan: Record<string, ColabOneLocaleTable> = {
    "GPU 显存 + 内存 PLE 协同装配（--fit on · KV q8_0）": {
      "zh-CN": s,
      "en-US": "GPU VRAM + RAM PLE hybrid (--fit on · KV q8_0)",
      "zh-TW": "GPU VRAM + 記憶體 PLE 協同裝配（--fit on · KV q8_0）",
      "zh-HK": "GPU 顯存 + 記憶體 PLE 協同裝配（--fit on · KV q8_0）",
      "ja-JP": "GPU VRAM + RAM PLE ハイブリッド（--fit on · KV q8_0）",
      "ko-KR": "GPU VRAM + RAM PLE 하이브리드 (--fit on · KV q8_0)",
    },
    "GPU 显存 + 系统内存智能混合装配（--fit on · KV q8_0）": {
      "zh-CN": s,
      "en-US": "GPU VRAM + system RAM hybrid (--fit on · KV q8_0)",
      "zh-TW": "GPU VRAM + 系統記憶體智慧混合裝配（--fit on · KV q8_0）",
      "zh-HK": "GPU 顯存 + 系統記憶體智能混合裝配（--fit on · KV q8_0）",
      "ja-JP": "GPU VRAM + システム RAM スマートハイブリッド（--fit on · KV q8_0）",
      "ko-KR": "GPU VRAM + 시스템 RAM 스마트 하이브리드 (--fit on · KV q8_0)",
    },
    "全量 GPU 驻留（FlashAttn · --fit on · KV q8_0）": {
      "zh-CN": s,
      "en-US": "Full GPU residency (FlashAttn · --fit on · KV q8_0)",
      "zh-TW": "全量 GPU 駐留（FlashAttn · --fit on · KV q8_0）",
      "zh-HK": "全量 GPU 駐留（FlashAttn · --fit on · KV q8_0）",
      "ja-JP": "フル GPU ロード（FlashAttn · --fit on · KV q8_0）",
      "ko-KR": "전체 GPU 로드 (FlashAttn · --fit on · KV q8_0)",
    },
    "GPU + 内存智能混合装配（--fit on · 非对称 KV q4_0/q8_0）": {
      "zh-CN": s,
      "en-US": "GPU + RAM hybrid (--fit on · asymmetric KV q4_0/q8_0)",
      "zh-TW": "GPU + 記憶體智慧混合裝配（--fit on · 非對稱 KV q4_0/q8_0）",
      "zh-HK": "GPU + 記憶體智能混合裝配（--fit on · 非對稱 KV q4_0/q8_0）",
      "ja-JP": "GPU + RAM ハイブリッド（--fit on · 非対称 KV q4_0/q8_0）",
      "ko-KR": "GPU + RAM 하이브리드 (--fit on · 비대칭 KV q4_0/q8_0)",
    },
    "GPU + 内存智能混合装配（--fit on · 紧凑 KV q4_0）": {
      "zh-CN": s,
      "en-US": "GPU + RAM hybrid (--fit on · compact KV q4_0)",
      "zh-TW": "GPU + 記憶體智慧混合裝配（--fit on · 精簡 KV q4_0）",
      "zh-HK": "GPU + 記憶體智能混合裝配（--fit on · 緊湊 KV q4_0）",
      "ja-JP": "GPU + RAM ハイブリッド（--fit on · コンパクト KV q4_0）",
      "ko-KR": "GPU + RAM 하이브리드 (--fit on · 컴팩트 KV q4_0)",
    },
    "GPU + 内存标准 KV 兼容装配（--fit on · KV f16）": {
      "zh-CN": s,
      "en-US": "GPU + RAM compatible KV hybrid (--fit on · KV f16)",
      "zh-TW": "GPU + 記憶體標準 KV 相容裝配（--fit on · KV f16）",
      "zh-HK": "GPU + 記憶體標準 KV 兼容裝配（--fit on · KV f16）",
      "ja-JP": "GPU + RAM 標準 KV 互換ロード（--fit on · KV f16）",
      "ko-KR": "GPU + RAM 표준 KV 호환 로드 (--fit on · KV f16)",
    },
    "GPU + 内存保守上下文装配（--fit on · 4K · KV q4_0）": {
      "zh-CN": s,
      "en-US": "GPU + RAM conservative context (--fit on · 4K · KV q4_0)",
      "zh-TW": "GPU + 記憶體保守上下文裝配（--fit on · 4K · KV q4_0）",
      "zh-HK": "GPU + 記憶體保守上下文裝配（--fit on · 4K · KV q4_0）",
      "ja-JP": "GPU + RAM 省メモリコンテキスト（--fit on · 4K · KV q4_0）",
      "ko-KR": "GPU + RAM 절약 컨텍스트 로드 (--fit on · 4K · KV q4_0)",
    },
  };
  return exactPlan[s]?.[loc] ?? s;
}

function translateFallbackFileWord(raw: string, loc: ColabOneLocale): string {
  if (raw !== "文件") return raw;
  return {
    "zh-CN": "文件",
    "en-US": "file",
    "zh-TW": "檔案",
    "zh-HK": "檔案",
    "ja-JP": "ファイル",
    "ko-KR": "파일",
  }[loc];
}

function translateFallbackUnknownWord(raw: string, loc: ColabOneLocale): string {
  if (raw !== "未知") return raw;
  return {
    "zh-CN": "未知",
    "en-US": "unknown",
    "zh-TW": "未知",
    "zh-HK": "未知",
    "ja-JP": "不明",
    "ko-KR": "알 수 없음",
  }[loc];
}

function translateAllocationMetaPart(part: string, loc: ColabOneLocale): string {
  const s = part.trim();
  if (!s) return "";
  if (s === "高内存") {
    return {
      "zh-CN": "高内存",
      "en-US": "High-RAM",
      "zh-TW": "高記憶體",
      "zh-HK": "高記憶體",
      "ja-JP": "ハイメモリ",
      "ko-KR": "고메모리",
    }[loc];
  }
  const rateMatch = s.match(/^([\d.]+) CU\/小时$/);
  if (rateMatch) {
    const rate = rateMatch[1];
    return {
      "zh-CN": s,
      "en-US": `${rate} CU/hr`,
      "zh-TW": `${rate} CU/小時`,
      "zh-HK": `${rate} CU/小時`,
      "ja-JP": `${rate} CU/時`,
      "ko-KR": `${rate} CU/시간`,
    }[loc];
  }
  return translateColabOnePlaceName(s, loc);
}

function translateSettingsSummaryPart(part: string, loc: ColabOneLocale): string {
  const s = part.trim();
  const gpuMatch = s.match(/^默认 GPU:\s*(.+)$/);
  if (gpuMatch) {
    const gpu = gpuMatch[1];
    return {
      "zh-CN": s,
      "en-US": `Default GPU: ${gpu}`,
      "zh-TW": `預設 GPU: ${gpu}`,
      "zh-HK": `預設 GPU: ${gpu}`,
      "ja-JP": `デフォルト GPU: ${gpu}`,
      "ko-KR": `기본 GPU: ${gpu}`,
    }[loc];
  }
  const idleMatch = s.match(/^空闲释放:\s*(\d+) 分钟$/);
  if (idleMatch) {
    const mins = idleMatch[1];
    return {
      "zh-CN": s,
      "en-US": `Idle release: ${mins} min`,
      "zh-TW": `閒置釋放: ${mins} 分鐘`,
      "zh-HK": `閒置釋放: ${mins} 分鐘`,
      "ja-JP": `アイドル解放: ${mins} 分`,
      "ko-KR": `유휴 해제: ${mins}분`,
    }[loc];
  }
  const thrMatch = s.match(/^下载并发:\s*(\d+) 线程$/);
  if (thrMatch) {
    const thr = thrMatch[1];
    return {
      "zh-CN": s,
      "en-US": `Download concurrency: ${thr} threads`,
      "zh-TW": `下載並行: ${thr} 執行緒`,
      "zh-HK": `下載並發: ${thr} 線程`,
      "ja-JP": `ダウンロード並列数: ${thr} スレッド`,
      "ko-KR": `다운로드 동시성: ${thr}스레드`,
    }[loc];
  }
  if (s === "已更新 Hugging Face Token") {
    return {
      "zh-CN": s,
      "en-US": "Updated Hugging Face Token",
      "zh-TW": "已更新 Hugging Face Token",
      "zh-HK": "已更新 Hugging Face Token",
      "ja-JP": "Hugging Face Token を更新",
      "ko-KR": "Hugging Face 토큰 업데이트됨",
    }[loc];
  }
  if (s === "已清除分配代理") {
    return {
      "zh-CN": s,
      "en-US": "Cleared allocation proxy",
      "zh-TW": "已清除分配代理",
      "zh-HK": "已清除分配代理",
      "ja-JP": "割り当てプロキシを削除",
      "ko-KR": "할당 프록시 삭제됨",
    }[loc];
  }
  if (s === "已开启仅分配代理") {
    return {
      "zh-CN": s,
      "en-US": "Allocation-only proxy enabled",
      "zh-TW": "已開啟僅分配代理",
      "zh-HK": "已開啟僅分配代理",
      "ja-JP": "割り当て専用プロキシを有効化",
      "ko-KR": "할당 전용 프록시 활성화됨",
    }[loc];
  }
  if (s === "已关闭仅分配代理") {
    return {
      "zh-CN": s,
      "en-US": "Allocation-only proxy disabled",
      "zh-TW": "已關閉僅分配代理",
      "zh-HK": "已關閉僅分配代理",
      "ja-JP": "割り当て専用プロキシを無効化",
      "ko-KR": "할당 전용 프록시 비활성화됨",
    }[loc];
  }
  const proxyActiveMatch = s.match(/^已(?:启用|开启)仅分配代理（(.+)）$/);
  if (proxyActiveMatch) {
    const label = proxyActiveMatch[1];
    return {
      "zh-CN": s,
      "en-US": `Allocation-only proxy active (${label})`,
      "zh-TW": `已啟用僅分配代理（${label}）`,
      "zh-HK": `已啟用僅分配代理（${label}）`,
      "ja-JP": `割り当て専用プロキシ有効（${label}）`,
      "ko-KR": `할당 전용 프록시 활성 (${label})`,
    }[loc];
  }
  const proxySavedMatch = s.match(/^已保存分配代理（(.+)）$/);
  if (proxySavedMatch) {
    const label = proxySavedMatch[1];
    return {
      "zh-CN": s,
      "en-US": `Saved allocation proxy (${label})`,
      "zh-TW": `已儲存分配代理（${label}）`,
      "zh-HK": `已儲存分配代理（${label}）`,
      "ja-JP": `割り当てプロキシを保存（${label}）`,
      "ko-KR": `할당 프록시 저장됨 (${label})`,
    }[loc];
  }
  return s;
}

function translateApiFinishPart(part: string, loc: ColabOneLocale): string {
  const s = part.trim();
  const outMatch = s.match(/^输出 (\d+) tokens$/);
  if (outMatch) {
    const n = outMatch[1];
    return {
      "zh-CN": s,
      "en-US": `${n} output tokens`,
      "zh-TW": `輸出 ${n} tokens`,
      "zh-HK": `輸出 ${n} tokens`,
      "ja-JP": `出力 ${n} tokens`,
      "ko-KR": `출력 ${n} tokens`,
    }[loc];
  }
  const compMatch = s.match(/^自动压缩 (\d+)→(\d+) tokens$/);
  if (compMatch) {
    const [, before, after] = compMatch;
    return {
      "zh-CN": s,
      "en-US": `auto-compacted ${before}→${after} tokens`,
      "zh-TW": `自動壓縮 ${before}→${after} tokens`,
      "zh-HK": `自動壓縮 ${before}→${after} tokens`,
      "ja-JP": `自動圧縮 ${before}→${after} tokens`,
      "ko-KR": `자동 압축 ${before}→${after} tokens`,
    }[loc];
  }
  const ttftMatch = s.match(/^首字 (.+)$/);
  if (ttftMatch) {
    const val = ttftMatch[1];
    return {
      "zh-CN": s,
      "en-US": `TTFT ${val}`,
      "zh-TW": `首字 ${val}`,
      "zh-HK": `首字 ${val}`,
      "ja-JP": `初回トークン ${val}`,
      "ko-KR": `첫 토큰 ${val}`,
    }[loc];
  }
  const durMatch = s.match(/^耗时 (.+)$/);
  if (durMatch) {
    const val = durMatch[1];
    return {
      "zh-CN": s,
      "en-US": `elapsed ${val}`,
      "zh-TW": `耗時 ${val}`,
      "zh-HK": `耗時 ${val}`,
      "ja-JP": `所要 ${val}`,
      "ko-KR": `소요 ${val}`,
    }[loc];
  }
  return s;
}

const COMFY_NODE_SCHEMA_ERROR_RE =
  /^节点 \S+ (?:必须为对象|缺少有效的 class_type|缺少有效的 inputs 对象|包含非法输入名称)$/;
const COMFY_NODE_ERROR_RE =
  /^节点 ([^\s(：]+?)(?:\(([^\r\n]+?)\))?:\s+(\S.*)$/s;

function parseComfyNodeError(
  trimmed: string,
): { id: string; cls: string; msg: string } | null {
  if (COMFY_NODE_SCHEMA_ERROR_RE.test(trimmed)) return null;
  if (!trimmed.startsWith("节点 ")) return null;

  let idx = 3;
  while (idx < trimmed.length) {
    const ch = trimmed[idx]!;
    if (/\s/.test(ch) || ch === "：" || ch === "(") break;
    idx++;
  }
  const rawId = trimmed.slice(3, idx);
  if (!rawId) return null;

  if (trimmed[idx] === "(") {
    let pos = idx + 1;
    let depth = 1;
    let closeIdx = -1;
    while (pos < trimmed.length) {
      const ch = trimmed[pos]!;
      if (ch === "\r" || ch === "\n") break;
      if (ch === "(") {
        depth++;
      } else if (ch === ")") {
        depth--;
        if (depth === 0) {
          closeIdx = pos;
          break;
        }
      }
      pos++;
    }
    if (closeIdx > idx + 1 && trimmed[closeIdx + 1] === ":") {
      const afterColon = trimmed.slice(closeIdx + 2);
      const wsMatch = afterColon.match(/^\s+/);
      if (wsMatch) {
        const msg = afterColon.slice(wsMatch[0].length);
        if (msg) {
          return {
            id: rawId,
            cls: trimmed.slice(idx + 1, closeIdx),
            msg,
          };
        }
      }
    }
    const fallback = trimmed.match(COMFY_NODE_ERROR_RE);
    if (fallback && fallback[1] && fallback[2] && fallback[3]) {
      return {
        id: fallback[1],
        cls: fallback[2],
        msg: fallback[3],
      };
    }
    return null;
  }

  if (!rawId.endsWith(":") || rawId.length <= 1) return null;
  const afterId = trimmed.slice(idx);
  const wsMatch = afterId.match(/^\s+/);
  if (!wsMatch) return null;
  const msg = afterId.slice(wsMatch[0].length);
  if (!msg) return null;
  return {
    id: rawId.slice(0, -1),
    cls: "",
    msg,
  };
}

function translateComfyErrorDetail(detail: string, loc: ColabOneLocale): string {
  const sep = loc === "en-US" || loc === "ko-KR" ? "; " : "；";
  const segments = detail.split("；");
  return segments
    .map((seg) => {
      const trimmed = seg.trim();
      const parsed = parseComfyNodeError(trimmed);
      if (parsed) {
        const { id, cls, msg } = parsed;
        const inner = translateInner(msg, loc);
        const nodePrefix = cls ? `${id} (${cls})` : id;
        switch (loc) {
          case "en-US":
            return `Node ${nodePrefix}: ${inner}`;
          case "zh-TW":
            return `節點 ${cls ? `${id}(${cls})` : id}: ${inner}`;
          case "zh-HK":
            return `節點 ${cls ? `${id}(${cls})` : id}: ${inner}`;
          case "ja-JP":
            return `ノード ${cls ? `${id}(${cls})` : id}: ${inner}`;
          case "ko-KR":
            return `노드 ${nodePrefix}: ${inner}`;
          default:
            return trimmed;
        }
      }
      return translateInner(trimmed, loc);
    })
    .join(sep);
}

function translateTaskTail(tail: string, loc: ColabOneLocale): string {
  const trimmed = tail.trim();
  if (!trimmed) return "";
  const parts = trimmed.split(" · ");
  if (parts.length >= 2) {
    // Prefer splitting message + error when both sides match independent rules
    // (prevents greedy trailing (.+) rules on message from swallowing an appended error).
    for (let i = parts.length - 1; i >= 1; i--) {
      const left = parts.slice(0, i).join(" · ");
      const right = parts.slice(i).join(" · ");
      const leftT = translateInner(left, loc);
      const rightT = translateInner(right, loc);
      if (leftT !== left && rightT !== right) {
        return `${leftT} · ${rightT}`;
      }
    }
  }
  const whole = translateInner(trimmed, loc);
  if (whole !== trimmed) return whole;
  if (parts.length >= 2) {
    for (let i = parts.length - 1; i >= 1; i--) {
      const left = parts.slice(0, i).join(" · ");
      const right = parts.slice(i).join(" · ");
      const leftT = translateInner(left, loc);
      const rightT = translateInner(right, loc);
      if (leftT !== left || rightT !== right) {
        return `${leftT} · ${rightT}`;
      }
    }
  }
  return whole;
}

const PATTERN_RULES: PatternRule[] = [
  // === Compound operation & remote task logs (recursive) ===
  {
    re: /^(.+)：完成$/,
    format: (m, loc) => {
      const title = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${title}: Completed`;
        case "zh-TW":
          return `${title}：完成`;
        case "zh-HK":
          return `${title}：完成`;
        case "ja-JP":
          return `${title}: 完了`;
        case "ko-KR":
          return `${title}: 완료`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(模型下载|模型加载|外部 API 对话|外部 API Responses|外部 API 上下文压缩|ComfyUI 启动|ComfyUI 工作流|ComfyUI 任务|远端任务)：(进行中|已完成|失败|已取消)(.*)$/s,
    format: (m, loc) => {
      const kind = TASK_KIND_LABELS[m[1]!]?.[loc] ?? m[1]!;
      const status = TASK_STATE_LABELS[m[2]!]?.[loc] ?? m[2]!;
      let rest = (m[3] || "").trim();
      let pctPrefix = "";
      const pctMatch = rest.match(/^·\s*(\d+%)(?:\s*·\s*(.*))?$/s);
      if (pctMatch) {
        pctPrefix = ` · ${pctMatch[1]}`;
        rest = (pctMatch[2] || "").trim();
      } else if (rest.startsWith("·")) {
        rest = rest.slice(1).trim();
      }
      const tailText = rest ? ` · ${translateTaskTail(rest, loc)}` : "";
      const colon = loc === "zh-TW" || loc === "zh-HK" ? "：" : ": ";
      return `${kind}${colon}${status}${pctPrefix}${tailText}`;
    },
  },

  // === console/internal/controller/controller.go ===
  {
    re: /^连接检查失败：(.+?)。客户端会自动重试，请检查网络连接。$/,
    format: (m, loc) => {
      const exc = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Connection check failed: ${exc}. The client will retry automatically; please check your network.`;
        case "zh-TW":
          return `連線檢查失敗：${exc}。用戶端將自動重試，請檢查網路連線。`;
        case "zh-HK":
          return `連線檢查失敗：${exc}。程式會自動重試，請檢查網絡連線。`;
        case "ja-JP":
          return `接続チェックに失敗しました: ${exc}。自動的に再試行します。ネットワーク接続を確認してください。`;
        case "ko-KR":
          return `연결 확인 실패: ${exc}. 클라이언트가 자동으로 재시도합니다. 네트워크 연결을 확인해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^空闲自动释放倒计时剩余 (\d+) 秒；继续操作可重置倒计时$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Idle auto-release in ${sec}s; any activity resets the timer`;
        case "zh-TW":
          return `閒置自動釋放倒數剩餘 ${sec} 秒；繼續操作可重設倒數`;
        case "zh-HK":
          return `閒置自動釋放倒數剩餘 ${sec} 秒；繼續操作可重設倒數`;
        case "ja-JP":
          return `アイドル自動解放まで残り ${sec} 秒です。操作するとタイマーがリセットされます`;
        case "ko-KR":
          return `유휴 자동 해제까지 ${sec}초 남았습니다. 작업을 계속하면 타이머가 초기화됩니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^服务返回 HTTP (\d+)，请检查授权和配置$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Service returned HTTP ${code}; please check authorization and configuration`;
        case "zh-TW":
          return `服務回傳 HTTP ${code}，請檢查授權與設定`;
        case "zh-HK":
          return `服務返回 HTTP ${code}，請檢查授權和配置`;
        case "ja-JP":
          return `サービスが HTTP ${code} を返しました。認可と設定を確認してください`;
        case "ko-KR":
          return `서비스가 HTTP ${code}을(를) 반환했습니다. 인증 및 설정을 확인해 주세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已切换当前工作实例：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Switched active workspace instance: ${ep}`;
        case "zh-TW":
          return `已切換目前工作執行個體：${ep}`;
        case "zh-HK":
          return `已切換目前工作實例：${ep}`;
        case "ja-JP":
          return `アクティブインスタンスを切り替えました: ${ep}`;
        case "ko-KR":
          return `현재 작업 인스턴스를 전환했습니다: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^准备新增远端 (\S+) 实例（保留现有运行实例）$/,
    format: (m, loc) => {
      const gpu = m[1];
      switch (loc) {
        case "en-US":
          return `Preparing to launch additional remote ${gpu} instance (keeping active instances)`;
        case "zh-TW":
          return `準備新增遠端 ${gpu} 執行個體（保留現有執行中個體）`;
        case "zh-HK":
          return `準備新增遠端 ${gpu} 實例（保留現有運行實例）`;
        case "ja-JP":
          return `追加のリモート ${gpu} インスタンス起動を準備中（既存インスタンスは維持）`;
        case "ko-KR":
          return `추가 원격 ${gpu} 인스턴스 시작 준비 중 (기존 실행 인스턴스 유지)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在同步空闲自动释放策略（(\d+) 分钟）$/,
    format: (m, loc) => {
      const mins = m[1];
      switch (loc) {
        case "en-US":
          return `Syncing idle auto-release policy (${mins} min)`;
        case "zh-TW":
          return `正在同步閒置自動釋放原則（${mins} 分鐘）`;
        case "zh-HK":
          return `正在同步閒置自動釋放策略（${mins} 分鐘）`;
        case "ja-JP":
          return `アイドル自動解放ポリシーを同期中（${mins} 分）`;
        case "ko-KR":
          return `유휴 자동 해제 정책 동기화 중 (${mins}분)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^云盘自动挂载未就绪，实例仍可正常使用：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Google Drive auto-mount not ready; instance remains usable: ${detail}`;
        case "zh-TW":
          return `雲端硬碟自動掛載未就緒，執行個體仍可正常使用：${detail}`;
        case "zh-HK":
          return `雲端硬碟自動掛載未就緒，實例仍可正常使用：${detail}`;
        case "ja-JP":
          return `Google ドライブの自動マウントは未完了ですがインスタンスは利用可能です: ${detail}`;
        case "ko-KR":
          return `Google 드라이브 자동 마운트가 준비되지 않았으나 인스턴스는 정상 사용 가능합니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^计费同步完成 · 余额 ([\d.]+) CU · 费率 ([\d.]+) CU\/小时 · 活跃 (\d+) 台$/,
    format: (m, loc) => {
      const [, bal, rate, cnt] = m;
      switch (loc) {
        case "en-US":
          return `Billing synced · Balance ${bal} CU · Rate ${rate} CU/hr · ${cnt} active`;
        case "zh-TW":
          return `計費同步完成 · 餘額 ${bal} CU · 費率 ${rate} CU/小時 · 執行中 ${cnt} 台`;
        case "zh-HK":
          return `計費同步完成 · 餘額 ${bal} CU · 費率 ${rate} CU/小時 · 活躍 ${cnt} 部`;
        case "ja-JP":
          return `課金同期完了 · 残高 ${bal} CU · レート ${rate} CU/時 · 稼働 ${cnt} 台`;
        case "ko-KR":
          return `과금 동기화 완료 · 잔액 ${bal} CU · 요율 ${rate} CU/시간 · 활성 ${cnt}대`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在清理残留实例：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Cleaning up leftover instance: ${ep}`;
        case "zh-TW":
          return `正在清理殘留執行個體：${ep}`;
        case "zh-HK":
          return `正在清理殘留實例：${ep}`;
        case "ja-JP":
          return `残留インスタンスをクリーンアップしています: ${ep}`;
        case "ko-KR":
          return `잔여 인스턴스를 정리하는 중: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已释放 (\S+)，当前切换至 (\S+)$/,
    format: (m, loc) => {
      const [, target, next] = m;
      switch (loc) {
        case "en-US":
          return `Released ${target}; switched active instance to ${next}`;
        case "zh-TW":
          return `已釋放 ${target}，目前已切換至 ${next}`;
        case "zh-HK":
          return `已釋放 ${target}，目前切換至 ${next}`;
        case "ja-JP":
          return `${target} を解放し、${next} に切り替えました`;
        case "ko-KR":
          return `${target} 해제 완료, 현재 인스턴스를 ${next}(으)로 전환했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已刷新账号状态 · 活跃实例 (\d+) 台 · 费率 ([\d.]+) CU\/小时$/,
    format: (m, loc) => {
      const [, cnt, rate] = m;
      switch (loc) {
        case "en-US":
          return `Account refreshed · ${cnt} active instances · Rate ${rate} CU/hr`;
        case "zh-TW":
          return `已重新整理帳戶狀態 · 執行中個體 ${cnt} 台 · 費率 ${rate} CU/小時`;
        case "zh-HK":
          return `已重新整理帳戶狀態 · 活躍實例 ${cnt} 部 · 費率 ${rate} CU/小時`;
        case "ja-JP":
          return `アカウント状態を更新 · 稼働インスタンス ${cnt} 台 · レート ${rate} CU/時`;
        case "ko-KR":
          return `계정 상태 새로고침 완료 · 활성 인스턴스 ${cnt}대 · 요율 ${rate} CU/시간`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^开始按确认清单停止 (\d+) 个远端实例$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `Stopping ${n} confirmed remote instance(s)`;
        case "zh-TW":
          return `開始依確認清單停止 ${n} 個遠端執行個體`;
        case "zh-HK":
          return `開始按確認清單停止 ${n} 個遠端實例`;
        case "ja-JP":
          return `確認済み一覧に従い ${n} 台のリモートインスタンスを停止しています`;
        case "ko-KR":
          return `확인된 목록에 따라 원격 인스턴스 ${n}대 중지 시작`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在停止远端任务并同步云盘（(.+)）$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Stopping remote tasks and syncing Google Drive (${ep})`;
        case "zh-TW":
          return `正在停止遠端任務並同步雲端硬碟（${ep}）`;
        case "zh-HK":
          return `正在停止遠端任務並同步雲端硬碟（${ep}）`;
        case "ja-JP":
          return `リモートタスクを停止しドライブを同期しています（${ep}）`;
        case "ko-KR":
          return `원격 작업 중지 및 Google 드라이브 동기화 중 (${ep})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端收尾完成（(.+)）$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Remote cleanup completed (${ep})`;
        case "zh-TW":
          return `遠端收尾完成（${ep}）`;
        case "zh-HK":
          return `遠端收尾完成（${ep}）`;
        case "ja-JP":
          return `リモート終了処理が完了しました（${ep}）`;
        case "ko-KR":
          return `원격 정리 완료 (${ep})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已处理确认的实例；当前账号仍有 (\d+) 个实例，请再次停止全部$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `Processed confirmed instances; ${n} instance(s) still remain on this account, please run Stop All again`;
        case "zh-TW":
          return `已處理確認的執行個體；目前帳戶仍有 ${n} 個執行個體，請再次全部停止`;
        case "zh-HK":
          return `已處理確認的實例；目前帳戶仍有 ${n} 個實例，請再次停止全部`;
        case "ja-JP":
          return `確認済みインスタンスを処理しましたが、アカウントに ${n} 台残っています。再度すべて停止を実行してください`;
        case "ko-KR":
          return `확인된 인스턴스를 처리했으나 계정에 ${n}대가 남아 있습니다. 모두 중지를 다시 실행해 주세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已停止所选的 (\d+) 个实例；当前账号剩余 (\d+) 个运行实例$/,
    format: (m, loc) => {
      const [, stopped, left] = m;
      switch (loc) {
        case "en-US":
          return `Stopped ${stopped} selected instance(s); ${left} running instance(s) remain on this account`;
        case "zh-TW":
          return `已停止所選的 ${stopped} 個執行個體；目前帳戶剩餘 ${left} 個執行中個體`;
        case "zh-HK":
          return `已停止所選的 ${stopped} 個實例；目前帳戶剩餘 ${left} 個運行實例`;
        case "ja-JP":
          return `選択した ${stopped} 台を停止しました（残りの稼働インスタンス: ${left} 台）`;
        case "ko-KR":
          return `선택한 ${stopped}대 중지 완료 (남은 실행 인스턴스 ${left}대)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在读取 Hugging Face 模型清单：(.+)$/,
    format: (m, loc) => {
      const repo = m[1];
      switch (loc) {
        case "en-US":
          return `Fetching Hugging Face model manifest: ${repo}`;
        case "zh-TW":
          return `正在讀取 Hugging Face 模型清單：${repo}`;
        case "zh-HK":
          return `正在讀取 Hugging Face 模型清單：${repo}`;
        case "ja-JP":
          return `Hugging Face モデル一覧を読み込んでいます: ${repo}`;
        case "ko-KR":
          return `Hugging Face 모델 목록을 불러오는 중: ${repo}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已解析模型清单：(.+?)（(\d+) 个文件，([\d.]+) GiB）$/,
    format: (m, loc) => {
      const [, name, count, gib] = m;
      switch (loc) {
        case "en-US":
          return `Parsed model manifest: ${name} (${count} files, ${gib} GiB)`;
        case "zh-TW":
          return `已解析模型清單：${name}（${count} 個檔案，${gib} GiB）`;
        case "zh-HK":
          return `已解析模型清單：${name}（${count} 個檔案，${gib} GiB）`;
        case "ja-JP":
          return `モデル一覧を解析しました: ${name}（${count} ファイル、${gib} GiB）`;
        case "ko-KR":
          return `모델 매니페스트 분석 완료: ${name} (${count}개 파일, ${gib} GiB)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已保存到本地模型库：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Saved to local model library: ${name}`;
        case "zh-TW":
          return `已儲存至本機模型庫：${name}`;
        case "zh-HK":
          return `已儲存至本地模型庫：${name}`;
        case "ja-JP":
          return `ローカルモデル一覧に保存しました: ${name}`;
        case "ko-KR":
          return `로컬 모델 라이브러리에 저장했습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已同步模型配置到远端实例：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Synced model config to remote instance: ${name}`;
        case "zh-TW":
          return `已同步模型設定至遠端執行個體：${name}`;
        case "zh-HK":
          return `已同步模型配置至遠端實例：${name}`;
        case "ja-JP":
          return `モデル設定をリモートインスタンスへ同期しました: ${name}`;
        case "ko-KR":
          return `원격 인스턴스에 모델 설정을 동기화했습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已将社区仓库加入我的模型：(.+)$/,
    format: (m, loc) => {
      const repo = m[1];
      switch (loc) {
        case "en-US":
          return `Saved community repo to My Models: ${repo}`;
        case "zh-TW":
          return `已將社群儲存庫加入我的模型：${repo}`;
        case "zh-HK":
          return `已將社群倉庫加入我的模型：${repo}`;
        case "ja-JP":
          return `コミュニティリポジトリをマイモデルに追加しました: ${repo}`;
        case "ko-KR":
          return `커뮤니티 저장소를 내 모델에 추가했습니다: ${repo}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已从我的模型移除仓库：(.+)$/,
    format: (m, loc) => {
      const repo = m[1];
      switch (loc) {
        case "en-US":
          return `Removed repo from My Models: ${repo}`;
        case "zh-TW":
          return `已從我的模型移除儲存庫：${repo}`;
        case "zh-HK":
          return `已從我的模型移除倉庫：${repo}`;
        case "ja-JP":
          return `マイモデルからリポジトリを削除しました: ${repo}`;
        case "ko-KR":
          return `내 모델에서 저장소를 제거했습니다: ${repo}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已从本地列表移除模型：(.+)$/,
    format: (m, loc) => {
      const id = m[1];
      switch (loc) {
        case "en-US":
          return `Removed model from local list: ${id}`;
        case "zh-TW":
          return `已從本機清單移除模型：${id}`;
        case "zh-HK":
          return `已從本地清單移除模型：${id}`;
        case "ja-JP":
          return `ローカル一覧からモデルを削除しました: ${id}`;
        case "ko-KR":
          return `로컬 목록에서 모델을 제거했습니다: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已保存设置（(.+)）$/,
    format: (m, loc) => {
      const parts = m[1]!
        .split(" · ")
        .map((p) => translateSettingsSummaryPart(p, loc))
        .join(" · ");
      switch (loc) {
        case "en-US":
          return `Saved settings (${parts})`;
        case "zh-TW":
          return `已儲存設定（${parts}）`;
        case "zh-HK":
          return `已儲存設定（${parts}）`;
        case "ja-JP":
          return `設定を保存しました（${parts}）`;
        case "ko-KR":
          return `설정을 저장했습니다 (${parts})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已向实例 (\S+) 同步空闲释放时长（(\d+) 分钟）$/,
    format: (m, loc) => {
      const [, ep, mins] = m;
      switch (loc) {
        case "en-US":
          return `Synced idle auto-release (${mins} min) to instance ${ep}`;
        case "zh-TW":
          return `已向執行個體 ${ep} 同步閒置釋放時間（${mins} 分鐘）`;
        case "zh-HK":
          return `已向實例 ${ep} 同步閒置釋放時長（${mins} 分鐘）`;
        case "ja-JP":
          return `インスタンス ${ep} へアイドル自動解放（${mins} 分）を同期しました`;
        case "ko-KR":
          return `인스턴스 ${ep}에 유휴 자동 해제(${mins}분)를 동기화했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^账号状态已刷新 · 余额 ([\d.]+) CU · 费率 ([\d.]+) CU\/小时 · 活跃 (\d+) 台$/,
    format: (m, loc) => {
      const [, bal, rate, cnt] = m;
      switch (loc) {
        case "en-US":
          return `Account refreshed · Balance ${bal} CU · Rate ${rate} CU/hr · ${cnt} active`;
        case "zh-TW":
          return `帳戶狀態已重新整理 · 餘額 ${bal} CU · 費率 ${rate} CU/小時 · 執行中 ${cnt} 台`;
        case "zh-HK":
          return `帳戶狀態已重新整理 · 餘額 ${bal} CU · 費率 ${rate} CU/小時 · 活躍 ${cnt} 部`;
        case "ja-JP":
          return `アカウント更新完了 · 残高 ${bal} CU · レート ${rate} CU/時 · 稼働 ${cnt} 台`;
        case "ko-KR":
          return `계정 상태 새로고침 완료 · 잔액 ${bal} CU · 요율 ${rate} CU/시간 · 활성 ${cnt}대`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已导入 ComfyUI 工作流：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Imported ComfyUI workflow: ${name}`;
        case "zh-TW":
          return `已匯入 ComfyUI 工作流程：${name}`;
        case "zh-HK":
          return `已匯入 ComfyUI 工作流：${name}`;
        case "ja-JP":
          return `ComfyUI ワークフローをインポートしました: ${name}`;
        case "ko-KR":
          return `ComfyUI 워크플로를 가져왔습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已离线保存 ComfyUI 工作流：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Saved ComfyUI workflow offline: ${name}`;
        case "zh-TW":
          return `已離線儲存 ComfyUI 工作流程：${name}`;
        case "zh-HK":
          return `已離線儲存 ComfyUI 工作流：${name}`;
        case "ja-JP":
          return `ComfyUI ワークフローをオフライン保存しました: ${name}`;
        case "ko-KR":
          return `ComfyUI 워크플로를 오프라인으로 저장했습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已删除 ComfyUI 工作流：(.+)$/,
    format: (m, loc) => {
      const wid = m[1];
      switch (loc) {
        case "en-US":
          return `Deleted ComfyUI workflow: ${wid}`;
        case "zh-TW":
          return `已刪除 ComfyUI 工作流程：${wid}`;
        case "zh-HK":
          return `已刪除 ComfyUI 工作流：${wid}`;
        case "ja-JP":
          return `ComfyUI ワークフローを削除しました: ${wid}`;
        case "ko-KR":
          return `ComfyUI 워크플로를 삭제했습니다: ${wid}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在上传 ComfyUI 输入素材：(.+)$/,
    format: (m, loc) => {
      const name = translateFallbackFileWord(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Uploading ComfyUI input asset: ${name}`;
        case "zh-TW":
          return `正在上傳 ComfyUI 輸入素材：${name}`;
        case "zh-HK":
          return `正在上傳 ComfyUI 輸入素材：${name}`;
        case "ja-JP":
          return `ComfyUI 入力素材をアップロードしています: ${name}`;
        case "ko-KR":
          return `ComfyUI 입력 자산 업로드 중: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^ComfyUI 输入素材上传完成：(.+)$/,
    format: (m, loc) => {
      const name = translateFallbackFileWord(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Uploaded ComfyUI input asset: ${name}`;
        case "zh-TW":
          return `ComfyUI 輸入素材上傳完成：${name}`;
        case "zh-HK":
          return `ComfyUI 輸入素材上傳完成：${name}`;
        case "ja-JP":
          return `ComfyUI 入力素材のアップロードが完了しました: ${name}`;
        case "ko-KR":
          return `ComfyUI 입력 자산 업로드 완료: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^请求运行 ComfyUI 工作流：(.+)$/,
    format: (m, loc) => {
      const wid = m[1];
      switch (loc) {
        case "en-US":
          return `Requesting ComfyUI workflow run: ${wid}`;
        case "zh-TW":
          return `請求執行 ComfyUI 工作流程：${wid}`;
        case "zh-HK":
          return `請求運行 ComfyUI 工作流：${wid}`;
        case "ja-JP":
          return `ComfyUI ワークフローの実行を要求しました: ${wid}`;
        case "ko-KR":
          return `ComfyUI 워크플로 실행 요청: ${wid}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在提交远端下载任务：(.+?)（(\d+) 线程(，完成后自动装载)?）$/,
    format: (m, loc) => {
      const [, name, threads, autoLoad] = m;
      switch (loc) {
        case "en-US":
          return `Submitting remote download: ${name} (${threads} threads${autoLoad ? ", auto-load after finish" : ""})`;
        case "zh-TW":
          return `正在提交遠端下載任務：${name}（${threads} 執行緒${autoLoad ? "，完成後自動載入" : ""}）`;
        case "zh-HK":
          return `正在提交遠端下載任務：${name}（${threads} 線程${autoLoad ? "，完成後自動載入" : ""}）`;
        case "ja-JP":
          return `リモートダウンロードを送信中: ${name}（${threads} スレッド${autoLoad ? "、完了後に自動ロード" : ""}）`;
        case "ko-KR":
          return `원격 다운로드 작업 제출 중: ${name} (${threads}스레드${autoLoad ? ", 완료 후 자동 로드" : ""})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端已启动模型下载：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Remote model download started: ${name}`;
        case "zh-TW":
          return `遠端已啟動模型下載：${name}`;
        case "zh-HK":
          return `遠端已啟動模型下載：${name}`;
        case "ja-JP":
          return `リモートでモデルダウンロードを開始しました: ${name}`;
        case "ko-KR":
          return `원격 모델 다운로드가 시작되었습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^释放远端实例 (.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Releasing remote instance ${ep}`;
        case "zh-TW":
          return `釋放遠端執行個體 ${ep}`;
        case "zh-HK":
          return `釋放遠端實例 ${ep}`;
        case "ja-JP":
          return `リモートインスタンス ${ep} を解放中`;
        case "ko-KR":
          return `원격 인스턴스 ${ep} 해제 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^请求下载模型 (.+?) 到远端 GPU（(\d+) 线程(，完成后自动装载)?）$/,
    format: (m, loc) => {
      const [, name, threads, autoLoad] = m;
      switch (loc) {
        case "en-US":
          return `Requesting download of ${name} to remote GPU (${threads} threads${autoLoad ? ", auto-load after finish" : ""})`;
        case "zh-TW":
          return `請求下載模型 ${name} 至遠端 GPU（${threads} 執行緒${autoLoad ? "，完成後自動載入" : ""}）`;
        case "zh-HK":
          return `請求下載模型 ${name} 到遠端 GPU（${threads} 線程${autoLoad ? "，完成後自動載入" : ""}）`;
        case "ja-JP":
          return `モデル ${name} のリモート GPU ダウンロードを要求（${threads} スレッド${autoLoad ? "、完了後に自動ロード" : ""}）`;
        case "ko-KR":
          return `원격 GPU에 모델 ${name} 다운로드 요청 (${threads}스레드${autoLoad ? ", 완료 후 자동 로드" : ""})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端下载任务已启动：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Remote download task started: ${name}`;
        case "zh-TW":
          return `遠端下載任務已啟動：${name}`;
        case "zh-HK":
          return `遠端下載任務已啟動：${name}`;
        case "ja-JP":
          return `リモートダウンロードタスクを開始しました: ${name}`;
        case "ko-KR":
          return `원격 다운로드 작업이 시작되었습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^请求装载模型 (.+?) 到远端显存$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Requesting load of ${name} into remote VRAM`;
        case "zh-TW":
          return `請求載入模型 ${name} 至遠端 VRAM`;
        case "zh-HK":
          return `請求載入模型 ${name} 到遠端顯存`;
        case "ja-JP":
          return `モデル ${name} のリモート VRAM ロードを要求しました`;
        case "ko-KR":
          return `원격 VRAM에 모델 ${name} 로드 요청`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端模型装载任务已启动：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Remote model load task started: ${name}`;
        case "zh-TW":
          return `遠端模型載入任務已啟動：${name}`;
        case "zh-HK":
          return `遠端模型載入任務已啟動：${name}`;
        case "ja-JP":
          return `リモートモデルロードタスクを開始しました: ${name}`;
        case "ko-KR":
          return `원격 모델 로드 작업이 시작되었습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^请求删除远端模型文件：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Requesting deletion of remote model files: ${name}`;
        case "zh-TW":
          return `請求刪除遠端模型檔案：${name}`;
        case "zh-HK":
          return `請求刪除遠端模型檔案：${name}`;
        case "ja-JP":
          return `リモートモデルファイルの削除を要求しました: ${name}`;
        case "ko-KR":
          return `원격 모델 파일 삭제 요청: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已删除远端模型文件：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Deleted remote model files: ${name}`;
        case "zh-TW":
          return `已刪除遠端模型檔案：${name}`;
        case "zh-HK":
          return `已刪除遠端模型檔案：${name}`;
        case "ja-JP":
          return `リモートモデルファイルを削除しました: ${name}`;
        case "ko-KR":
          return `원격 모델 파일을 삭제했습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },

  // === console/internal/provider/provider.go ===
  {
    re: /^查询 Google Colab 实例状态：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Checking Google Colab instance status: ${ep}`;
        case "zh-TW":
          return `正在查詢 Google Colab 執行個體狀態：${ep}`;
        case "zh-HK":
          return `正在查詢 Google Colab 實例狀態：${ep}`;
        case "ja-JP":
          return `Google Colab インスタンス状態を確認しています: ${ep}`;
        case "ko-KR":
          return `Google Colab 인스턴스 상태 조회 중: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^向 Google Colab 发送释放请求：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Sending release request to Google Colab: ${ep}`;
        case "zh-TW":
          return `正在向 Google Colab 送出釋放請求：${ep}`;
        case "zh-HK":
          return `正在向 Google Colab 發送釋放請求：${ep}`;
        case "ja-JP":
          return `Google Colab へ解放リクエストを送信しています: ${ep}`;
        case "ko-KR":
          return `Google Colab에 해제 요청 전송 중: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^释放请求未获直接确认，改为轮询核实停止状态：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Release not immediately confirmed; polling to verify stop state: ${ep}`;
        case "zh-TW":
          return `釋放請求未獲直接確認，改為輪詢核實停止狀態：${ep}`;
        case "zh-HK":
          return `釋放請求未獲直接確認，改為輪詢核實停止狀態：${ep}`;
        case "ja-JP":
          return `解放応答を直接確認できなかったため停止状態をポーリング確認しています: ${ep}`;
        case "ko-KR":
          return `해제 요청이 즉시 확인되지 않아 중지 상태를 폴링하여 확인하는 중: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^实例已不在 Colab 活跃列表中：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Instance is no longer in Colab's active list: ${ep}`;
        case "zh-TW":
          return `執行個體已不在 Colab 執行中清單：${ep}`;
        case "zh-HK":
          return `實例已不在 Colab 活躍清單中：${ep}`;
        case "ja-JP":
          return `インスタンスは Colab の稼働一覧から消えました: ${ep}`;
        case "ko-KR":
          return `인스턴스가 더 이상 Colab 활성 목록에 없습니다: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已确认实例停止并清理本机会话：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Confirmed instance stopped and cleared local session: ${ep}`;
        case "zh-TW":
          return `已確認執行個體停止並清理本機工作階段：${ep}`;
        case "zh-HK":
          return `已確認實例停止並清理本地會話：${ep}`;
        case "ja-JP":
          return `インスタンス停止を確認しローカルセッションを消去しました: ${ep}`;
        case "ko-KR":
          return `인스턴스 중지를 확인하고 로컬 세션을 정리했습니다: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^等待 Google Colab 确认停止计费：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Waiting for Google Colab to confirm billing stopped: ${ep}`;
        case "zh-TW":
          return `等待 Google Colab 確認停止計費：${ep}`;
        case "zh-HK":
          return `等待 Google Colab 確認停止扣費：${ep}`;
        case "ja-JP":
          return `Google Colab の課金停止確認を待機しています: ${ep}`;
        case "ko-KR":
          return `Google Colab의 과금 중지 확인 대기 중: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^仍在等待释放确认（第 (\d+)\/(\d+) 次查询）：(.+)$/,
    format: (m, loc) => {
      const [, cur, total, ep] = m;
      switch (loc) {
        case "en-US":
          return `Still waiting for release confirmation (check ${cur}/${total}): ${ep}`;
        case "zh-TW":
          return `仍在等待釋放確認（第 ${cur}/${total} 次查詢）：${ep}`;
        case "zh-HK":
          return `仍在等待釋放確認（第 ${cur}/${total} 次查詢）：${ep}`;
        case "ja-JP":
          return `解放確認を引き続き待機中（${cur}/${total} 回目）: ${ep}`;
        case "ko-KR":
          return `해제 확인 대기 중 (${cur}/${total}회 확인): ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在查询账号运行实例（待停止 (\d+) 个）$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `Querying running instances on account (${n} to stop)`;
        case "zh-TW":
          return `正在查詢帳戶執行中個體（待停止 ${n} 個）`;
        case "zh-HK":
          return `正在查詢帳戶運行實例（待停止 ${n} 個）`;
        case "ja-JP":
          return `アカウントの稼働インスタンスを照会中（停止対象 ${n} 台）`;
        case "ko-KR":
          return `계정의 실행 중인 인스턴스 조회 중 (중지 대상 ${n}대)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在停止实例：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Stopping instance: ${ep}`;
        case "zh-TW":
          return `正在停止執行個體：${ep}`;
        case "zh-HK":
          return `正在停止實例：${ep}`;
        case "ja-JP":
          return `インスタンスを停止中: ${ep}`;
        case "ko-KR":
          return `인스턴스 중지 중: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^停止请求未获确认，正在重新查询：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Stop request not confirmed; re-checking: ${ep}`;
        case "zh-TW":
          return `停止請求未獲確認，正在重新查詢：${ep}`;
        case "zh-HK":
          return `停止請求未獲確認，正在重新查詢：${ep}`;
        case "ja-JP":
          return `停止リクエスト未確認のため再照会しています: ${ep}`;
        case "ko-KR":
          return `중지 요청이 확인되지 않아 다시 조회 중: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已确认停止实例：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Confirmed instance stopped: ${ep}`;
        case "zh-TW":
          return `已確認停止執行個體：${ep}`;
        case "zh-HK":
          return `已確認停止實例：${ep}`;
        case "ja-JP":
          return `インスタンスの停止を確認しました: ${ep}`;
        case "ko-KR":
          return `인스턴스 중지 확인됨: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^等待 Google Colab 确认停止计费（剩余 (\d+) 个）$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `Waiting for Google Colab to confirm billing stopped (${n} remaining)`;
        case "zh-TW":
          return `等待 Google Colab 確認停止計費（剩餘 ${n} 個）`;
        case "zh-HK":
          return `等待 Google Colab 確認停止扣費（剩餘 ${n} 個）`;
        case "ja-JP":
          return `Google Colab の課金停止確認を待機中（残り ${n} 台）`;
        case "ko-KR":
          return `Google Colab 과금 중지 확인 대기 중 (${n}대 남음)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^仍有 (\d+) 个实例未确认停止，可能仍在计费，请重试$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `${n} instance(s) not yet confirmed stopped and may still be billing; please retry`;
        case "zh-TW":
          return `仍有 ${n} 個執行個體未確認停止，可能仍在計費，請重試`;
        case "zh-HK":
          return `仍有 ${n} 個實例未確認停止，可能仍在扣費，請重試`;
        case "ja-JP":
          return `${n} 台の停止が未確認で課金継続中の可能性があります。再試行してください`;
        case "ko-KR":
          return `${n}대의 인스턴스 중지가 확인되지 않아 계속 과금 중일 수 있습니다. 다시 시도해 주세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^向 Google Colab 请求分配 (\S+) GPU（(高内存规格|默认规格)）$/,
    format: (m, loc) => {
      const [, gpu, shapeRaw] = m;
      const isHighRam = shapeRaw === "高内存规格";
      switch (loc) {
        case "en-US":
          return `Requesting ${gpu} GPU from Google Colab (${isHighRam ? "High-RAM shape" : "Standard shape"})`;
        case "zh-TW":
          return `正在向 Google Colab 請求分配 ${gpu} GPU（${isHighRam ? "高記憶體規格" : "預設規格"}）`;
        case "zh-HK":
          return `正在向 Google Colab 請求分配 ${gpu} GPU（${isHighRam ? "高記憶體規格" : "預設規格"}）`;
        case "ja-JP":
          return `Google Colab に ${gpu} GPU の割り当てを要求しています（${isHighRam ? "ハイメモリ構成" : "標準構成"}）`;
        case "ko-KR":
          return `Google Colab에 ${gpu} GPU 할당 요청 중 (${isHighRam ? "고메모리 구성" : "기본 구성"})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已保存实例会话与控制令牌：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Saved instance session and control token: ${ep}`;
        case "zh-TW":
          return `已儲存執行個體工作階段與控制權杖：${ep}`;
        case "zh-HK":
          return `已儲存實例會話與控制令牌：${ep}`;
        case "ja-JP":
          return `インスタンスセッションと制御トークンを保存しました: ${ep}`;
        case "ko-KR":
          return `인스턴스 세션 및 제어 토큰을 저장했습니다: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 Jupyter 内核已响应（耗时 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Remote Jupyter kernel responded (${sec}s)`;
        case "zh-TW":
          return `遠端 Jupyter 核心已回應（耗時 ${sec} 秒）`;
        case "zh-HK":
          return `遠端 Jupyter 核心已回應（耗時 ${sec} 秒）`;
        case "ja-JP":
          return `リモート Jupyter カーネルが応答しました（${sec} 秒）`;
        case "ko-KR":
          return `원격 Jupyter 커널 응답 확인 (${sec}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^仍在等待远端 Jupyter 内核响应（已等待 (\d+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Still waiting for remote Jupyter kernel (${sec}s elapsed)`;
        case "zh-TW":
          return `仍在等待遠端 Jupyter 核心回應（已等待 ${sec} 秒）`;
        case "zh-HK":
          return `仍在等待遠端 Jupyter 核心回應（已等待 ${sec} 秒）`;
        case "ja-JP":
          return `リモート Jupyter カーネルの応答を待機中（${sec} 秒経過）`;
        case "ko-KR":
          return `원격 Jupyter 커널 응답 대기 중 (${sec}초 경과)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^检测到已连接实例，直接复用：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Detected connected instance; reusing: ${ep}`;
        case "zh-TW":
          return `偵測到已連線執行個體，直接重複使用：${ep}`;
        case "zh-HK":
          return `檢測到已連線實例，直接複用：${ep}`;
        case "ja-JP":
          return `接続済みインスタンスを検出したため再利用します: ${ep}`;
        case "ko-KR":
          return `이미 연결된 인스턴스를 감지하여 재사용합니다: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前余额 ([\d.]+) CU · 活跃实例 (\d+) 台$/,
    format: (m, loc) => {
      const [, bal, cnt] = m;
      switch (loc) {
        case "en-US":
          return `Balance ${bal} CU · ${cnt} active instance(s)`;
        case "zh-TW":
          return `目前餘額 ${bal} CU · 執行中個體 ${cnt} 台`;
        case "zh-HK":
          return `目前餘額 ${bal} CU · 活躍實例 ${cnt} 部`;
        case "ja-JP":
          return `現在の残高 ${bal} CU · 稼働インスタンス ${cnt} 台`;
        case "ko-KR":
          return `현재 잔액 ${bal} CU · 활성 인스턴스 ${cnt}대`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google 算力紧张，正在排队分配 (\S+)（第 (\d+)\/(\d+) 次）$/,
    format: (m, loc) => {
      const [, gpu, cur, total] = m;
      switch (loc) {
        case "en-US":
          return `Google GPU capacity is tight; queuing for ${gpu} (attempt ${cur}/${total})`;
        case "zh-TW":
          return `Google 算力緊張，正在排隊分配 ${gpu}（第 ${cur}/${total} 次）`;
        case "zh-HK":
          return `Google 算力緊張，正在排隊分配 ${gpu}（第 ${cur}/${total} 次）`;
        case "ja-JP":
          return `Google の計算リソースが逼迫中 · ${gpu} の割り当てを待機中（${cur}/${total} 回目）`;
        case "ko-KR":
          return `Google 연산 리소스 부족 · ${gpu} 할당 대기 중 (${cur}/${total}회 시도)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google 分配服务繁忙，正在重试分配 (\S+)（第 (\d+)\/(\d+) 次）$/,
    format: (m, loc) => {
      const [, gpu, cur, total] = m;
      switch (loc) {
        case "en-US":
          return `Google allocation service is busy; retrying ${gpu} (attempt ${cur}/${total})`;
        case "zh-TW":
          return `Google 分配服務忙碌，正在重試分配 ${gpu}（第 ${cur}/${total} 次）`;
        case "zh-HK":
          return `Google 分配服務繁忙，正在重試分配 ${gpu}（第 ${cur}/${total} 次）`;
        case "ja-JP":
          return `Google の割り当てサービスが混雑中 · ${gpu} の割り当てを再試行中（${cur}/${total} 回目）`;
        case "ko-KR":
          return `Google 할당 서비스 혼잡 · ${gpu} 할당 재시도 중 (${cur}/${total}회 시도)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在排队分配 (\S+)（算力池较紧张 · 第 (\d+)\/(\d+) 次）$/,
    format: (m, loc) => {
      const [, gpu, cur, total] = m;
      switch (loc) {
        case "en-US":
          return `Queuing for ${gpu} (pool congested · attempt ${cur}/${total})`;
        case "zh-TW":
          return `正在排隊分配 ${gpu}（算力池較緊張 · 第 ${cur}/${total} 次）`;
        case "zh-HK":
          return `正在排隊分配 ${gpu}（算力池較緊張 · 第 ${cur}/${total} 次）`;
        case "ja-JP":
          return `${gpu} の割り当てを待機中（計算リソース混雑 · ${cur}/${total} 回目）`;
        case "ko-KR":
          return `${gpu} 할당 대기 중 (리소스 혼잡 · ${cur}/${total}회 시도)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已手动触发立即重试，跳过等待立即尝试分配 (\S+)（第 (\d+)\/(\d+) 次）$/,
    format: (m, loc) => {
      const [, gpu, cur, total] = m;
      switch (loc) {
        case "en-US":
          return `Manual retry triggered; skipping wait to allocate ${gpu} now (attempt ${cur}/${total})`;
        case "zh-TW":
          return `已手動觸發立即重試，略過等待立即嘗試分配 ${gpu}（第 ${cur}/${total} 次）`;
        case "zh-HK":
          return `已手動觸發立即重試，跳過等待立即嘗試分配 ${gpu}（第 ${cur}/${total} 次）`;
        case "ja-JP":
          return `手動再試行を実行しました。待機をスキップして ${gpu} の割り当てを即時試行します（${cur}/${total} 回目）`;
        case "ko-KR":
          return `수동 재시도가 요청되어 대기를 건너뛰고 ${gpu} 할당을 즉시 시도합니다 (${cur}/${total}회 시도)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^启动已取消，正在释放刚分配的实例：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Startup cancelled; releasing newly allocated instance: ${ep}`;
        case "zh-TW":
          return `啟動已取消，正在釋放剛分配的執行個體：${ep}`;
        case "zh-HK":
          return `啟動已取消，正在釋放剛分配的實例：${ep}`;
        case "ja-JP":
          return `起動がキャンセルされました。割り当て済みのインスタンスを解放しています: ${ep}`;
        case "ko-KR":
          return `시작이 취소되어 방금 할당된 인스턴스를 해제하는 중: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在分配 (\S+)（第 (\d+)\/(\d+) 次）$/,
    format: (m, loc) => {
      const [, gpu, cur, total] = m;
      switch (loc) {
        case "en-US":
          return `Allocating ${gpu} (attempt ${cur}/${total})`;
        case "zh-TW":
          return `正在分配 ${gpu}（第 ${cur}/${total} 次）`;
        case "zh-HK":
          return `正在分配 ${gpu}（第 ${cur}/${total} 次）`;
        case "ja-JP":
          return `${gpu} を割り当て中（${cur}/${total} 回目）`;
        case "ko-KR":
          return `${gpu} 할당 중 (${cur}/${total}회 시도)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在分配 (.+)$/,
    format: (m, loc) => {
      const gpu = m[1];
      switch (loc) {
        case "en-US":
          return `Allocating ${gpu}`;
        case "zh-TW":
          return `正在分配 ${gpu}`;
        case "zh-HK":
          return `正在分配 ${gpu}`;
        case "ja-JP":
          return `${gpu} を割り当て中`;
        case "ko-KR":
          return `${gpu} 할당 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 当前 (\S+) 算力紧张，暂未分配出 GPU（(?:耗时 (\d+(?:\.\d+)?) 秒 · )?(HTTP \d+)），(\d+) 秒后自动重试（第 (\d+)\/(\d+) 次）$/,
    format: (m, loc) => {
      const [, gpu, elapsed, code, sec, cur, total] = m;
      const detail = elapsed
        ? {
            "zh-CN": `耗时 ${elapsed} 秒 · ${code}`,
            "en-US": `${elapsed}s · ${code}`,
            "zh-TW": `耗時 ${elapsed} 秒 · ${code}`,
            "zh-HK": `耗時 ${elapsed} 秒 · ${code}`,
            "ja-JP": `${elapsed} 秒経過 · ${code}`,
            "ko-KR": `${elapsed}초 경과 · ${code}`,
          }[loc]
        : code!;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} capacity is currently tight and no GPU was allocated (${detail}); retrying in ${sec}s (attempt ${cur}/${total})`;
        case "zh-TW":
          return `Google Colab 目前 ${gpu} 算力緊張，暫未分配出 GPU（${detail}），${sec} 秒後自動重試（第 ${cur}/${total} 次）`;
        case "zh-HK":
          return `Google Colab 目前 ${gpu} 算力緊張，暫未分配出 GPU（${detail}），${sec} 秒後自動重試（第 ${cur}/${total} 次）`;
        case "ja-JP":
          return `現在 Google Colab の ${gpu} 計算リソースが逼迫しており、GPU が割り当てられませんでした（${detail}）。${sec} 秒後に自動再試行します（${cur}/${total} 回目）`;
        case "ko-KR":
          return `현재 Google Colab의 ${gpu} 연산 리소스가 부족하여 GPU가 할당되지 않았습니다 (${detail}). ${sec}초 후 자동 재시도합니다 (${cur}/${total}회)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 当前 (\S+) 分配服务繁忙，暂未分配出 GPU（(?:耗时 (\d+(?:\.\d+)?) 秒 · )?(HTTP \d+)），(\d+) 秒后自动重试（第 (\d+)\/(\d+) 次）$/,
    format: (m, loc) => {
      const [, gpu, elapsed, code, sec, cur, total] = m;
      const detail = elapsed
        ? {
            "zh-CN": `耗时 ${elapsed} 秒 · ${code}`,
            "en-US": `${elapsed}s · ${code}`,
            "zh-TW": `耗時 ${elapsed} 秒 · ${code}`,
            "zh-HK": `耗時 ${elapsed} 秒 · ${code}`,
            "ja-JP": `${elapsed} 秒経過 · ${code}`,
            "ko-KR": `${elapsed}초 경과 · ${code}`,
          }[loc]
        : code!;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} allocation service is currently busy and no GPU was allocated (${detail}); retrying in ${sec}s (attempt ${cur}/${total})`;
        case "zh-TW":
          return `Google Colab 目前 ${gpu} 分配服務忙碌，暫未分配出 GPU（${detail}），${sec} 秒後自動重試（第 ${cur}/${total} 次）`;
        case "zh-HK":
          return `Google Colab 目前 ${gpu} 分配服務繁忙，暫未分配出 GPU（${detail}），${sec} 秒後自動重試（第 ${cur}/${total} 次）`;
        case "ja-JP":
          return `現在 Google Colab の ${gpu} 割り当てサービスが混雑しており、GPU が割り当てられませんでした（${detail}）。${sec} 秒後に自動再試行します（${cur}/${total} 回目）`;
        case "ko-KR":
          return `현재 Google Colab의 ${gpu} 할당 서비스가 혼잡하여 GPU가 할당되지 않았습니다 (${detail}). ${sec}초 후 자동 재시도합니다 (${cur}/${total}회)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 分配 (\S+) GPU 暂时繁忙或超时（(?:耗时 (\d+(?:\.\d+)?) 秒 · )?(HTTP \d+|请求超时|网络异常)），(\d+) 秒后自动重试（第 (\d+)\/(\d+) 次）$/,
    format: (m, loc) => {
      const [, gpu, elapsed, reasonRaw, sec, cur, total] = m;
      const reason =
        reasonRaw === "请求超时"
          ? {
              "zh-CN": "请求超时",
              "en-US": "request timeout",
              "zh-TW": "請求逾時",
              "zh-HK": "請求逾時",
              "ja-JP": "タイムアウト",
              "ko-KR": "요청 시간 초과",
            }[loc]
          : reasonRaw === "网络异常"
            ? {
                "zh-CN": "网络异常",
                "en-US": "network error",
                "zh-TW": "網路異常",
                "zh-HK": "網絡異常",
                "ja-JP": "ネットワークエラー",
                "ko-KR": "네트워크 오류",
              }[loc]
            : reasonRaw!;
      const detail = elapsed
        ? {
            "zh-CN": `耗时 ${elapsed} 秒 · ${reason}`,
            "en-US": `${elapsed}s · ${reason}`,
            "zh-TW": `耗時 ${elapsed} 秒 · ${reason}`,
            "zh-HK": `耗時 ${elapsed} 秒 · ${reason}`,
            "ja-JP": `${elapsed} 秒経過 · ${reason}`,
            "ko-KR": `${elapsed}초 경과 · ${reason}`,
          }[loc]
        : reason;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} allocation temporarily busy or timed out (${detail}); retrying in ${sec}s (attempt ${cur}/${total})`;
        case "zh-TW":
          return `Google Colab 分配 ${gpu} GPU 暫時忙碌或逾時（${detail}），${sec} 秒後自動重試（第 ${cur}/${total} 次）`;
        case "zh-HK":
          return `Google Colab 分配 ${gpu} GPU 暫時繁忙或逾時（${detail}），${sec} 秒後自動重試（第 ${cur}/${total} 次）`;
        case "ja-JP":
          return `Google Colab の ${gpu} GPU 割り当てが一時的に混雑またはタイムアウトしました（${detail}）。${sec} 秒後に自動再試行します（${cur}/${total} 回目）`;
        case "ko-KR":
          return `Google Colab ${gpu} GPU 할당이 일시적으로 혼잡하거나 시간 초과되었습니다 (${detail}). ${sec}초 후 자동 재시도합니다 (${cur}/${total}회)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 侧 (\S+) 算力持续紧张，暂未放出空闲 GPU，客户端会继续自动排队；也可稍后改选 (.+)$/,
    format: (m, loc) => {
      const [, gpu, altGpus] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} capacity remains tight with no idle GPUs released yet; ColabOne will keep queuing automatically, or you can switch to ${altGpus} later.`;
        case "zh-TW":
          return `Google Colab 端 ${gpu} 算力持續緊張，暫未釋出空閒 GPU，用戶端會繼續自動排隊；也可稍後改選 ${altGpus}`;
        case "zh-HK":
          return `Google Colab 端 ${gpu} 算力持續緊張，暫未釋出空閒 GPU，程式會繼續自動排隊；亦可稍後改選 ${altGpus}`;
        case "ja-JP":
          return `Google Colab 側の ${gpu} 計算リソースが引き続き逼迫しており空き GPU が確保できていません。自動で待機を継続します。必要に応じて後ほど ${altGpus} へ切り替えてください`;
        case "ko-KR":
          return `Google Colab 측 ${gpu} 연산 리소스가 계속 부족해 여유 GPU가 확보되지 않았습니다. 자동으로 대기열을 유지하며, 원하시면 나중에 ${altGpus}로 변경할 수 있습니다.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前 Google Colab 的 (\S+) 算力池较紧张，正在排队调度；可继续等待，或稍后改选 (.+)$/,
    format: (m, loc) => {
      const [, gpu, altGpus] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} capacity is currently congested and queued; you can keep waiting or switch to ${altGpus} later.`;
        case "zh-TW":
          return `目前 Google Colab 的 ${gpu} 算力池較緊張，正在排隊調度；可繼續等待，或稍後改選 ${altGpus}`;
        case "zh-HK":
          return `目前 Google Colab 的 ${gpu} 算力池較緊張，正在排隊調度；可繼續等待，或稍後改選 ${altGpus}`;
        case "ja-JP":
          return `現在 Google Colab の ${gpu} 計算リソースが混雑しており順番待ち中です。そのまま待機するか、後ほど ${altGpus} へ切り替えてください`;
        case "ko-KR":
          return `현재 Google Colab의 ${gpu} 리소스가 혼잡하여 대기 중입니다. 계속 기다리거나 나중에 ${altGpus}로 변경해 보세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 分配 (\S+) GPU 网络连接中断，已自动重试 (\d+) 次仍未成功，请检查网络或代理后重试$/,
    format: (m, loc) => {
      const [, gpu, count] = m;
      switch (loc) {
        case "en-US":
          return `Network connection interrupted while allocating Google Colab ${gpu} GPU after ${count} automatic retries. Check your network or proxy and try again.`;
        case "zh-TW":
          return `Google Colab 分配 ${gpu} GPU 網路連線中斷，已自動重試 ${count} 次仍未成功，請檢查網路或代理後重試`;
        case "zh-HK":
          return `Google Colab 分配 ${gpu} GPU 網絡連接中斷，已自動重試 ${count} 次仍未成功，請檢查網絡或代理後重試`;
        case "ja-JP":
          return `Google Colab の ${gpu} GPU 割り当て中にネットワーク接続が中断されました（${count} 回自動再試行しても失敗）。接続またはプロキシを確認して再試行してください`;
        case "ko-KR":
          return `Google Colab ${gpu} GPU 할당 중 네트워크 연결이 끊겼습니다 (${count}회 자동 재시도 실패). 네트워크 또는 프록시를 확인한 뒤 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 分配 (\S+) GPU 失败，请检查网络连接后重试$/,
    format: (m, loc) => {
      const gpu = m[1];
      switch (loc) {
        case "en-US":
          return `Failed to allocate Google Colab ${gpu} GPU. Check your network connection and try again.`;
        case "zh-TW":
          return `Google Colab 分配 ${gpu} GPU 失敗，請檢查網路連線後重試`;
        case "zh-HK":
          return `Google Colab 分配 ${gpu} GPU 失敗，請檢查網絡連接後重試`;
        case "ja-JP":
          return `Google Colab の ${gpu} GPU 割り当てに失敗しました。ネットワーク接続を確認して再試行してください`;
        case "ko-KR":
          return `Google Colab ${gpu} GPU 할당에 실패했습니다. 네트워크 연결을 확인한 뒤 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^保存实例会话失败，已尝试释放刚分配的实例：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to save instance session; attempted to release the newly allocated instance: ${detail}`;
        case "zh-TW":
          return `儲存執行個體工作階段失敗，已嘗試釋放剛分配的執行個體：${detail}`;
        case "zh-HK":
          return `儲存實例工作階段失敗，已嘗試釋放剛分配的實例：${detail}`;
        case "ja-JP":
          return `インスタンスセッションの保存に失敗しました。割り当てたインスタンスの解放を試行しました: ${detail}`;
        case "ko-KR":
          return `인스턴스 세션 저장에 실패하여 새로 할당된 인스턴스 해제를 시도했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已通过状态核验确认 (\S+) GPU 实例分配成功$/,
    format: (m, loc) => {
      const gpu = m[1];
      switch (loc) {
        case "en-US":
          return `Confirmed ${gpu} GPU instance allocation via status reconciliation`;
        case "zh-TW":
          return `已透過狀態核驗確認 ${gpu} GPU 執行個體分配成功`;
        case "zh-HK":
          return `已透過狀態核驗確認 ${gpu} GPU 實例分配成功`;
        case "ja-JP":
          return `ステータス照合により ${gpu} GPU インスタンスの割り当て完了を確認しました`;
        case "ko-KR":
          return `상태 확인을 통해 ${gpu} GPU 인스턴스 할당이 완료되었음을 확인했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 当前 (\S+) 算力紧张，暂未分配出 GPU（HTTP (\d+)），已自动重试 (\d+) 次仍未排到可用资源，请稍后重试或切换其他 GPU 型号$/,
    format: (m, loc) => {
      const [, gpu, code, retries] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} capacity is currently tight and no GPU was allocated (HTTP ${code}) after ${retries} automatic retries. Please retry later or switch to another GPU.`;
        case "zh-TW":
          return `Google Colab 目前 ${gpu} 算力緊張，暫未分配出 GPU（HTTP ${code}），已自動重試 ${retries} 次仍未排到可用資源，請稍後重試或切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 目前 ${gpu} 算力緊張，暫未分配出 GPU（HTTP ${code}），已自動重試 ${retries} 次仍未排到可用資源，請稍後再試或切換其他 GPU 型號`;
        case "ja-JP":
          return `現在 Google Colab の ${gpu} 計算リソースが逼迫しており、GPU を割り当てられませんでした（HTTP ${code}）。${retries} 回自動再試行しましたが確保できませんでした。しばらく待ってから再試行するか別の GPU をお試しください`;
        case "ko-KR":
          return `현재 Google Colab의 ${gpu} 연산 리소스가 부족하여 GPU가 할당되지 않았습니다 (HTTP ${code}). ${retries}회 자동 재시도 후에도 여유 리소스를 확보하지 못했습니다. 잠시 후 다시 시도하거나 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 当前 (\S+) 算力紧张，暂未分配出 GPU（HTTP (\d+)），请稍后重试或切换其他 GPU 型号$/,
    format: (m, loc) => {
      const [, gpu, code] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} capacity is currently tight and no GPU was allocated (HTTP ${code}). Please retry later or switch to another GPU.`;
        case "zh-TW":
          return `Google Colab 目前 ${gpu} 算力緊張，暫未分配出 GPU（HTTP ${code}），請稍後重試或切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 目前 ${gpu} 算力緊張，暫未分配出 GPU（HTTP ${code}），請稍後再試或切換其他 GPU 型號`;
        case "ja-JP":
          return `現在 Google Colab の ${gpu} 計算リソースが逼迫しており、GPU を割り当てられませんでした（HTTP ${code}）。しばらく待ってから再試行するか別の GPU をお試しください`;
        case "ko-KR":
          return `현재 Google Colab의 ${gpu} 연산 리소스가 부족하여 GPU가 할당되지 않았습니다 (HTTP ${code}). 잠시 후 다시 시도하거나 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 当前 (\S+) 分配服务繁忙或超时，暂未分配出 GPU（HTTP (\d+)），已自动重试 (\d+) 次仍未成功，请稍后重试或切换其他 GPU 型号$/,
    format: (m, loc) => {
      const [, gpu, code, retries] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} allocation service is currently busy or timed out and no GPU was allocated (HTTP ${code}) after ${retries} automatic retries. Please retry later or switch to another GPU.`;
        case "zh-TW":
          return `Google Colab 目前 ${gpu} 分配服務忙碌或逾時，暫未分配出 GPU（HTTP ${code}），已自動重試 ${retries} 次仍未成功，請稍後重試或切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 目前 ${gpu} 分配服務繁忙或逾時，暫未分配出 GPU（HTTP ${code}），已自動重試 ${retries} 次仍未成功，請稍後再試或切換其他 GPU 型號`;
        case "ja-JP":
          return `現在 Google Colab の ${gpu} 割り当てサービスが混雑またはタイムアウトしており、GPU を割り当てられませんでした（HTTP ${code}）。${retries} 回自動再試行しましたが失敗しました。しばらく待ってから再試行するか別の GPU をお試しください`;
        case "ko-KR":
          return `현재 Google Colab의 ${gpu} 할당 서비스가 혼잡하거나 시간 초과되어 GPU가 할당되지 않았습니다 (HTTP ${code}). ${retries}회 자동 재시도 후에도 실패했습니다. 잠시 후 다시 시도하거나 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 当前 (\S+) 分配服务繁忙或超时，暂未分配出 GPU（HTTP (\d+)），请稍后重试或切换其他 GPU 型号$/,
    format: (m, loc) => {
      const [, gpu, code] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} allocation service is currently busy or timed out and no GPU was allocated (HTTP ${code}). Please retry later or switch to another GPU.`;
        case "zh-TW":
          return `Google Colab 目前 ${gpu} 分配服務忙碌或逾時，暫未分配出 GPU（HTTP ${code}），請稍後重試或切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 目前 ${gpu} 分配服務繁忙或逾時，暫未分配出 GPU（HTTP ${code}），請稍後再試或切換其他 GPU 型號`;
        case "ja-JP":
          return `現在 Google Colab の ${gpu} 割り当てサービスが混雑またはタイムアウトしており、GPU を割り当てられませんでした（HTTP ${code}）。しばらく待ってから再試行するか別の GPU をお試しください`;
        case "ko-KR":
          return `현재 Google Colab의 ${gpu} 할당 서비스가 혼잡하거나 시간 초과되어 GPU가 할당되지 않았습니다 (HTTP ${code}). 잠시 후 다시 시도하거나 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 分配 (\S+) GPU 暂时不可用（HTTP (\d+)，该型号可能算力紧张或后端分配超时），已自动重试 (\d+) 次仍未成功，请稍后重试或切换其他 GPU 型号$/,
    format: (m, loc) => {
      const [, gpu, code, retries] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} GPU allocation is temporarily unavailable (HTTP ${code}; capacity may be tight or backend provisioning timed out) after ${retries} automatic retries. Please retry later or switch to another GPU.`;
        case "zh-TW":
          return `Google Colab 分配 ${gpu} GPU 暫時無法使用（HTTP ${code}，該型號可能算力緊張或後端分配逾時），已自動重試 ${retries} 次仍未成功，請稍後重試或切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 分配 ${gpu} GPU 暫時無法使用（HTTP ${code}，該型號可能算力緊張或後端分配逾時），已自動重試 ${retries} 次仍未成功，請稍後再試或切換其他 GPU 型號`;
        case "ja-JP":
          return `Google Colab の ${gpu} GPU 割り当てが一時的に利用できません（HTTP ${code}、在庫逼迫またはバックエンド割り当てタイムアウト）。${retries} 回自動再試行しましたが失敗しました。しばらく待ってから再試行するか別の GPU をお試しください`;
        case "ko-KR":
          return `Google Colab ${gpu} GPU 할당을 일시적으로 사용할 수 없습니다 (HTTP ${code}, 용량 부족 또는 백엔드 할당 시간 초과). ${retries}회 자동 재시도 후에도 실패했습니다. 잠시 후 다시 시도하거나 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 分配 (\S+) GPU 暂时不可用（HTTP (\d+)，该型号可能算力紧张或后端分配超时），请稍后重试或切换其他 GPU 型号$/,
    format: (m, loc) => {
      const [, gpu, code] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} GPU allocation is temporarily unavailable (HTTP ${code}; capacity may be tight or backend provisioning timed out). Please retry later or switch to another GPU.`;
        case "zh-TW":
          return `Google Colab 分配 ${gpu} GPU 暫時無法使用（HTTP ${code}，該型號可能算力緊張或後端分配逾時），請稍後重試或切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 分配 ${gpu} GPU 暫時無法使用（HTTP ${code}，該型號可能算力緊張或後端分配逾時），請稍後再試或切換其他 GPU 型號`;
        case "ja-JP":
          return `Google Colab の ${gpu} GPU 割り当てが一時的に利用できません（HTTP ${code}、在庫逼迫またはバックエンド割り当てタイムアウト）。しばらく待ってから再試行するか別の GPU をお試しください`;
        case "ko-KR":
          return `Google Colab ${gpu} GPU 할당을 일시적으로 사용할 수 없습니다 (HTTP ${code}, 용량 부족 또는 백엔드 할당 시간 초과). 잠시 후 다시 시도하거나 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 分配 (\S+) GPU 请求超时，已自动重试 (\d+) 次仍未成功，请稍后重试或切换其他 GPU 型号$/,
    format: (m, loc) => {
      const [, gpu, retries] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} GPU allocation timed out after ${retries} automatic retries. Please retry later or switch to another GPU.`;
        case "zh-TW":
          return `Google Colab 分配 ${gpu} GPU 請求逾時，已自動重試 ${retries} 次仍未成功，請稍後重試或切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 分配 ${gpu} GPU 請求逾時，已自動重試 ${retries} 次仍未成功，請稍後再試或切換其他 GPU 型號`;
        case "ja-JP":
          return `Google Colab の ${gpu} GPU 割り当てリクエストがタイムアウトしました（${retries} 回自動再試行済み）。しばらく待ってから再試行するか別の GPU をお試しください`;
        case "ko-KR":
          return `Google Colab ${gpu} GPU 할당 요청이 ${retries}회 자동 재시도 후에도 시간 초과되었습니다. 잠시 후 다시 시도하거나 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 分配 (\S+) GPU 请求超时，请稍后重试或切换其他 GPU 型号$/,
    format: (m, loc) => {
      const gpu = m[1];
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} GPU allocation timed out. Please retry later or switch to another GPU.`;
        case "zh-TW":
          return `Google Colab 分配 ${gpu} GPU 請求逾時，請稍後重試或切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 分配 ${gpu} GPU 請求逾時，請稍後再試或切換其他 GPU 型號`;
        case "ja-JP":
          return `Google Colab の ${gpu} GPU 割り当てリクエストがタイムアウトしました。しばらく待ってから再試行するか別の GPU をお試しください`;
        case "ko-KR":
          return `Google Colab ${gpu} GPU 할당 요청이 시간 초과되었습니다. 잠시 후 다시 시도하거나 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 拒绝分配 (\S+) GPU（HTTP 412）：当前账号活跃实例已达上限或受配额限制，请先释放已有实例或切换其他 GPU 型号$/,
    format: (m, loc) => {
      const gpu = m[1];
      switch (loc) {
        case "en-US":
          return `Google Colab refused ${gpu} GPU allocation (HTTP 412): active instance limit or temporary quota cap reached. Release an existing instance or try another GPU.`;
        case "zh-TW":
          return `Google Colab 拒絕分配 ${gpu} GPU（HTTP 412）：目前帳號執行中個體已達上限或受配額限制，請先釋放現有執行個體或切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 拒絕分配 ${gpu} GPU（HTTP 412）：目前帳號活躍實例已達上限或受配額限制，請先釋放現有實例或切換其他 GPU 型號`;
        case "ja-JP":
          return `Google Colab が ${gpu} GPU の割り当てを拒否しました（HTTP 412）: 稼働中のインスタンス上限または一時的なクォータ制限に達しています。既存インスタンスを解放するか別の GPU をお試しください`;
        case "ko-KR":
          return `Google Colab이 ${gpu} GPU 할당을 거부했습니다 (HTTP 412): 활성 인스턴스 한도 또는 일시적 할당량 제한에 도달했습니다. 기존 인스턴스를 해제하거나 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 拒绝分配 (\S+) GPU（HTTP 400）：当前账号可能暂无该型号配额或权限，请切换其他 GPU 型号$/,
    format: (m, loc) => {
      const gpu = m[1];
      switch (loc) {
        case "en-US":
          return `Google Colab rejected ${gpu} GPU allocation (HTTP 400): your account may not have quota or entitlement for this GPU. Try another GPU.`;
        case "zh-TW":
          return `Google Colab 拒絕分配 ${gpu} GPU（HTTP 400）：目前帳號可能暫無該型號配額或權限，請切換其他 GPU 型號`;
        case "zh-HK":
          return `Google Colab 拒絕分配 ${gpu} GPU（HTTP 400）：目前帳號可能暫無該型號配額或權限，請切換其他 GPU 型號`;
        case "ja-JP":
          return `Google Colab が ${gpu} GPU の割り当てを拒否しました（HTTP 400）: このアカウントには当該モデルの利用枠や権限がない可能性があります。別の GPU をお試しください`;
        case "ko-KR":
          return `Google Colab이 ${gpu} GPU 할당을 거부했습니다 (HTTP 400): 현재 계정에 해당 모델의 할당량이나 권한이 없을 수 있습니다. 다른 GPU를 선택해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 授权校验失败（HTTP (\d+)），请在设置中重新登录后重试$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Google Colab authorization check failed (HTTP ${code}). Please sign in again in Settings and retry.`;
        case "zh-TW":
          return `Google Colab 授權驗證失敗（HTTP ${code}），請在設定中重新登入後重試`;
        case "zh-HK":
          return `Google Colab 授權驗證失敗（HTTP ${code}），請在設定中重新登入後再試`;
        case "ja-JP":
          return `Google Colab の認証確認に失敗しました（HTTP ${code}）。設定から再ログインしてやり直してください`;
        case "ko-KR":
          return `Google Colab 인증 확인에 실패했습니다 (HTTP ${code}). 설정에서 다시 로그인한 후 재시도해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 分配 (\S+) GPU 失败（HTTP (\d+)），请稍后重试$/,
    format: (m, loc) => {
      const [, gpu, code] = m;
      switch (loc) {
        case "en-US":
          return `Google Colab ${gpu} GPU allocation failed (HTTP ${code}). Please retry later.`;
        case "zh-TW":
          return `Google Colab 分配 ${gpu} GPU 失敗（HTTP ${code}），請稍後重試`;
        case "zh-HK":
          return `Google Colab 分配 ${gpu} GPU 失敗（HTTP ${code}），請稍後再試`;
        case "ja-JP":
          return `Google Colab の ${gpu} GPU 割り当てに失敗しました（HTTP ${code}）。しばらくしてから再試行してください`;
        case "ko-KR":
          return `Google Colab ${gpu} GPU 할당에 실패했습니다 (HTTP ${code}). 잠시 후 다시 시도해 주세요.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端初始化异常：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Remote bootstrap exception: ${detail}`;
        case "zh-TW":
          return `遠端初始化例外：${detail}`;
        case "zh-HK":
          return `遠端初始化異常：${detail}`;
        case "ja-JP":
          return `リモート初期化エラー: ${detail}`;
        case "ko-KR":
          return `원격 초기화 예외: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端初始化未完成：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Remote initialization did not complete: ${detail}`;
        case "zh-TW":
          return `遠端初始化未完成：${detail}`;
        case "zh-HK":
          return `遠端初始化未完成：${detail}`;
        case "ja-JP":
          return `リモート初期化が完了しませんでした: ${detail}`;
        case "ko-KR":
          return `원격 초기화가 완료되지 않았습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已分配实例 (\S+?)(?:（(.+)）)?$/,
    format: (m, loc) => {
      const ep = m[1];
      const rawMeta = m[2];
      const translatedMeta = rawMeta
        ? rawMeta
            .split(" · ")
            .map((part) => translateAllocationMetaPart(part, loc))
            .filter(Boolean)
            .join(" · ")
        : "";
      switch (loc) {
        case "en-US":
          return translatedMeta
            ? `Allocated instance ${ep} (${translatedMeta})`
            : `Allocated instance ${ep}`;
        case "zh-TW":
          return translatedMeta
            ? `已分配執行個體 ${ep}（${translatedMeta}）`
            : `已分配執行個體 ${ep}`;
        case "zh-HK":
          return translatedMeta
            ? `已分配實例 ${ep}（${translatedMeta}）`
            : `已分配實例 ${ep}`;
        case "ja-JP":
          return translatedMeta
            ? `インスタンス ${ep} を割り当てました（${translatedMeta}）`
            : `インスタンス ${ep} を割り当てました`;
        case "ko-KR":
          return translatedMeta
            ? `인스턴스 ${ep} 할당 완료 (${translatedMeta})`
            : `인스턴스 ${ep} 할당 완료`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 Jupyter 内核已就绪（耗时 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Remote Jupyter kernel ready (${sec}s)`;
        case "zh-TW":
          return `遠端 Jupyter 核心已就緒（耗時 ${sec} 秒）`;
        case "zh-HK":
          return `遠端 Jupyter 核心已就緒（耗時 ${sec} 秒）`;
        case "ja-JP":
          return `リモート Jupyter カーネルの準備が完了しました（${sec} 秒）`;
        case "ko-KR":
          return `원격 Jupyter 커널 준비 완료 (${sec}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已打包远端管理服务（(\d+) 个文件，(\d+) KB）$/,
    format: (m, loc) => {
      const [, files, kb] = m;
      switch (loc) {
        case "en-US":
          return `Packaged remote control service (${files} files, ${kb} KB)`;
        case "zh-TW":
          return `已封裝遠端管理服務（${files} 個檔案，${kb} KB）`;
        case "zh-HK":
          return `已打包遠端管理服務（${files} 個檔案，${kb} KB）`;
        case "ja-JP":
          return `リモート管理サービスをパッケージ化しました（${files} ファイル、${kb} KB）`;
        case "ko-KR":
          return `원격 관리 서비스 패키징 완료 (${files}개 파일, ${kb} KB)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^启动过程出错，正在自动回滚释放刚分配的实例：(.+)$/,
    format: (m, loc) => {
      const ep = m[1];
      switch (loc) {
        case "en-US":
          return `Launch error; automatically rolling back and releasing newly allocated instance: ${ep}`;
        case "zh-TW":
          return `啟動過程出錯，正在自動回復並釋放剛分配的執行個體：${ep}`;
        case "zh-HK":
          return `啟動過程出錯，正在自動回滾釋放剛分配的實例：${ep}`;
        case "ja-JP":
          return `起動中にエラーが発生したため、割り当てたインスタンスを自動解放しています: ${ep}`;
        case "ko-KR":
          return `시작 중 오류가 발생하여 방금 할당된 인스턴스를 자동으로 롤백 해제하는 중: ${ep}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已打包新版远端服务（(\d+) 个文件），正在上传$/,
    format: (m, loc) => {
      const files = m[1];
      switch (loc) {
        case "en-US":
          return `Packaged updated remote service (${files} files); uploading`;
        case "zh-TW":
          return `已封裝新版遠端服務（${files} 個檔案），正在上傳`;
        case "zh-HK":
          return `已打包新版遠端服務（${files} 個檔案），正在上傳`;
        case "ja-JP":
          return `新しいリモートサービスをパッケージ化しました（${files} ファイル）。アップロードしています`;
        case "ko-KR":
          return `새 원격 서비스 패키징 완료 (${files}개 파일), 업로드 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端服务返回 HTTP (\d+)$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Remote service returned HTTP ${code}`;
        case "zh-TW":
          return `遠端服務回傳 HTTP ${code}`;
        case "zh-HK":
          return `遠端服務返回 HTTP ${code}`;
        case "ja-JP":
          return `リモートサービスが HTTP ${code} を返しました`;
        case "ko-KR":
          return `원격 서비스가 HTTP ${code}을(를) 반환했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在启动 (\S+) GPU(?:\.{3}|…)?$/,
    format: (m, loc) => {
      const gpu = m[1];
      switch (loc) {
        case "en-US":
          return `Starting ${gpu} GPU…`;
        case "zh-TW":
          return `正在啟動 ${gpu} GPU…`;
        case "zh-HK":
          return `正在啟動 ${gpu} GPU…`;
        case "ja-JP":
          return `${gpu} GPU を起動しています…`;
        case "ko-KR":
          return `${gpu} GPU를 시작하고 있습니다…`;
        default:
          return m[0];
      }
    },
  },

  // === console/internal/shortcuts/shortcuts.go ===
  {
    re: /^移除快捷方式失败：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to remove shortcut: ${detail}`;
        case "zh-TW":
          return `移除捷徑失敗：${detail}`;
        case "zh-HK":
          return `移除捷徑失敗：${detail}`;
        case "ja-JP":
          return `ショートカットの削除に失敗しました: ${detail}`;
        case "ko-KR":
          return `바로가기 제거 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },

  // === server/comfy_proxy.py ===
  {
    re: /^画布公网地址验证超时；固定域名请映射到 http:\/\/127\.0\.0\.1:(\d+)，并检查 Cloudflare 路由与访问策略$/,
    format: (m, loc) => {
      const port = m[1];
      switch (loc) {
        case "en-US":
          return `Canvas public URL check timed out; map your custom domain to http://127.0.0.1:${port} and check Cloudflare routing and access policies`;
        case "zh-TW":
          return `畫布公開位址驗證逾時；請將固定網域對應至 http://127.0.0.1:${port}，並檢查 Cloudflare 路由與存取原則`;
        case "zh-HK":
          return `畫布公網位址驗證逾時；固定域名請映射到 http://127.0.0.1:${port}，並檢查 Cloudflare 路由與存取策略`;
        case "ja-JP":
          return `キャンバスの公開 URL 検証がタイムアウトしました。固定ドメインを http://127.0.0.1:${port} に向け、Cloudflare のルートとアクセスポリシーを確認してください`;
        case "ko-KR":
          return `캔버스 공용 주소 확인 시간이 초과되었습니다. 고정 도메인을 http://127.0.0.1:${port}에 매핑하고 Cloudflare 라우팅 및 액세스 정책을 확인해 주세요`;
        default:
          return m[0];
      }
    },
  },

  // === server/comfyui.py ===
  {
    re: /^(.+?)\s*不能是符号链接$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!, loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} cannot be a symbolic link`;
        case "zh-TW":
          return `${label}${zhSep}不可為符號連結`;
        case "zh-HK":
          return `${label}${zhSep}不能是符號連結`;
        case "ja-JP":
          return `${label} にシンボリックリンクは使用できません`;
        case "ko-KR":
          return `${label}은(는) 심볼릭 링크일 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^输入文件超过大小限制（最大 (\d+) MB）$/,
    format: (m, loc) => {
      const mb = m[1];
      switch (loc) {
        case "en-US":
          return `Input file exceeds size limit (max ${mb} MB)`;
        case "zh-TW":
          return `輸入檔案超過大小限制（最大 ${mb} MB）`;
        case "zh-HK":
          return `輸入檔案超過大小限制（最大 ${mb} MB）`;
        case "ja-JP":
          return `入力ファイルがサイズ上限を超えています（最大 ${mb} MB）`;
        case "ko-KR":
          return `입력 파일이 크기 제한을 초과했습니다 (최대 ${mb} MB)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的输入文件格式：(.+)$/,
    format: (m, loc) => {
      const ext = translateFallbackUnknownWord(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Unsupported input file format: ${ext}`;
        case "zh-TW":
          return `不支援的輸入檔案格式：${ext}`;
        case "zh-HK":
          return `不支援的輸入檔案格式：${ext}`;
        case "ja-JP":
          return `サポートされていない入力ファイル形式です: ${ext}`;
        case "ko-KR":
          return `지원하지 않는 입력 파일 형식입니다: ${ext}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的输出文件格式：(.+)$/,
    format: (m, loc) => {
      const ext = translateFallbackUnknownWord(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Unsupported output file format: ${ext}`;
        case "zh-TW":
          return `不支援的輸出檔案格式：${ext}`;
        case "zh-HK":
          return `不支援的輸出檔案格式：${ext}`;
        case "ja-JP":
          return `サポートされていない出力ファイル形式です: ${ext}`;
        case "ko-KR":
          return `지원하지 않는 출력 파일 형식입니다: ${ext}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 ComfyUI 准备失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Remote ComfyUI preparation failed: ${detail}`;
        case "zh-TW":
          return `遠端 ComfyUI 準備失敗：${detail}`;
        case "zh-HK":
          return `遠端 ComfyUI 準備失敗：${detail}`;
        case "ja-JP":
          return `リモート ComfyUI の準備に失敗しました: ${detail}`;
        case "ko-KR":
          return `원격 ComfyUI 준비 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^ComfyUI 进程异常退出（退出码 (-?\d+)）$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `ComfyUI process exited unexpectedly (exit code ${code})`;
        case "zh-TW":
          return `ComfyUI 處理程序異常結束（結束代碼 ${code}）`;
        case "zh-HK":
          return `ComfyUI 進程異常退出（退出碼 ${code}）`;
        case "ja-JP":
          return `ComfyUI プロセスが異常終了しました（終了コード ${code}）`;
        case "ko-KR":
          return `ComfyUI 프로세스가 비정상 종료되었습니다 (종료 코드 ${code})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用远端已安装的 ComfyUI (.+)$/,
    format: (m, loc) => {
      const tag = m[1];
      switch (loc) {
        case "en-US":
          return `Reusing installed remote ComfyUI ${tag}`;
        case "zh-TW":
          return `重複使用遠端已安裝的 ComfyUI ${tag}`;
        case "zh-HK":
          return `複用遠端已安裝的 ComfyUI ${tag}`;
        case "ja-JP":
          return `インストール済みのリモート ComfyUI ${tag} を再利用します`;
        case "ko-KR":
          return `원격에 설치된 ComfyUI ${tag} 재사용 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^首次安装远端 ComfyUI (.+)$/,
    format: (m, loc) => {
      const tag = m[1];
      switch (loc) {
        case "en-US":
          return `Installing remote ComfyUI ${tag} for the first time`;
        case "zh-TW":
          return `首次安裝遠端 ComfyUI ${tag}`;
        case "zh-HK":
          return `首次安裝遠端 ComfyUI ${tag}`;
        case "ja-JP":
          return `リモート ComfyUI ${tag} を初回インストール中`;
        case "ko-KR":
          return `원격 ComfyUI ${tag} 최초 설치 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 ComfyUI (.+?) 安装完成$/,
    format: (m, loc) => {
      const tag = m[1];
      switch (loc) {
        case "en-US":
          return `Remote ComfyUI ${tag} installation completed`;
        case "zh-TW":
          return `遠端 ComfyUI ${tag} 安裝完成`;
        case "zh-HK":
          return `遠端 ComfyUI ${tag} 安裝完成`;
        case "ja-JP":
          return `リモート ComfyUI ${tag} のインストールが完了しました`;
        case "ko-KR":
          return `원격 ComfyUI ${tag} 설치가 완료되었습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已生成模型路径配置，正在启动远端 ComfyUI（端口 (\d+)）$/,
    format: (m, loc) => {
      const port = m[1];
      switch (loc) {
        case "en-US":
          return `Generated model path config; starting remote ComfyUI (port ${port})`;
        case "zh-TW":
          return `已產生模型路徑設定，正在啟動遠端 ComfyUI（連接埠 ${port}）`;
        case "zh-HK":
          return `已生成模型路徑配置，正在啟動遠端 ComfyUI（連接埠 ${port}）`;
        case "ja-JP":
          return `モデルパス設定を生成しました。リモート ComfyUI を起動しています（ポート ${port}）`;
        case "ko-KR":
          return `모델 경로 설정 생성 완료, 원격 ComfyUI 시작 중 (포트 ${port})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^退出码 (-?\d+)$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `exit code ${code}`;
        case "zh-TW":
          return `結束代碼 ${code}`;
        case "zh-HK":
          return `退出碼 ${code}`;
        case "ja-JP":
          return `終了コード ${code}`;
        case "ko-KR":
          return `종료 코드 ${code}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^ComfyUI 启动失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `ComfyUI startup failed: ${detail}`;
        case "zh-TW":
          return `ComfyUI 啟動失敗：${detail}`;
        case "zh-HK":
          return `ComfyUI 啟動失敗：${detail}`;
        case "ja-JP":
          return `ComfyUI の起動に失敗しました: ${detail}`;
        case "ko-KR":
          return `ComfyUI 시작 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 ComfyUI 引擎已就绪（耗时 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Remote ComfyUI engine ready (${sec}s)`;
        case "zh-TW":
          return `遠端 ComfyUI 引擎已就緒（耗時 ${sec} 秒）`;
        case "zh-HK":
          return `遠端 ComfyUI 引擎已就緒（耗時 ${sec} 秒）`;
        case "ja-JP":
          return `リモート ComfyUI エンジンの準備が完了しました（${sec} 秒）`;
        case "ko-KR":
          return `원격 ComfyUI 엔진 준비 완료 (${sec}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在等待 ComfyUI 完成节点初始化（已耗时 (\d+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Waiting for ComfyUI node initialization (${sec}s elapsed)`;
        case "zh-TW":
          return `正在等待 ComfyUI 完成節點初始化（已耗時 ${sec} 秒）`;
        case "zh-HK":
          return `正在等待 ComfyUI 完成節點初始化（已耗時 ${sec} 秒）`;
        case "ja-JP":
          return `ComfyUI のノード初期化完了を待機しています（${sec} 秒経過）`;
        case "ko-KR":
          return `ComfyUI 노드 초기화 완료 대기 중 (${sec}초 경과)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在向 ComfyUI 上传 (\d+) 个输入素材$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `Uploading ${n} input asset(s) to ComfyUI`;
        case "zh-TW":
          return `正在向 ComfyUI 上傳 ${n} 個輸入素材`;
        case "zh-HK":
          return `正在向 ComfyUI 上傳 ${n} 個輸入素材`;
        case "ja-JP":
          return `ComfyUI へ ${n} 件の入力素材をアップロードしています`;
        case "ko-KR":
          return `ComfyUI에 입력 자산 ${n}개 업로드 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^提交工作流：(.+)$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Submitting workflow: ${name}`;
        case "zh-TW":
          return `提交工作流程：${name}`;
        case "zh-HK":
          return `提交工作流：${name}`;
        case "ja-JP":
          return `ワークフローを送信中: ${name}`;
        case "ko-KR":
          return `워크플로 제출 중: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^ComfyUI 拒绝了工作流：(.+)$/s,
    format: (m, loc) => {
      const detail = translateComfyErrorDetail(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `ComfyUI rejected workflow: ${detail}`;
        case "zh-TW":
          return `ComfyUI 拒絕了工作流程：${detail}`;
        case "zh-HK":
          return `ComfyUI 拒絕了工作流：${detail}`;
        case "ja-JP":
          return `ComfyUI がワークフローを拒否しました: ${detail}`;
        case "ko-KR":
          return `ComfyUI가 워크플로를 거부했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流节点校验失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateComfyErrorDetail(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Workflow node validation failed: ${detail}`;
        case "zh-TW":
          return `工作流程節點驗證失敗：${detail}`;
        case "zh-HK":
          return `工作流節點校驗失敗：${detail}`;
        case "ja-JP":
          return `ワークフローノードの検証に失敗しました: ${detail}`;
        case "ko-KR":
          return `워크플로 노드 검증 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^ComfyUI 未返回有效任务编号：(.+)$/s,
    format: (m, loc) => {
      const detail = translateComfyErrorDetail(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `ComfyUI did not return a valid prompt ID: ${detail}`;
        case "zh-TW":
          return `ComfyUI 未回傳有效任務編號：${detail}`;
        case "zh-HK":
          return `ComfyUI 未返回有效任務編號：${detail}`;
        case "ja-JP":
          return `ComfyUI から有効なジョブ ID が返されませんでした: ${detail}`;
        case "ko-KR":
          return `ComfyUI가 유효한 작업 ID를 반환하지 않았습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流已入队：(.+?)（任务 (\S+)）$/,
    format: (m, loc) => {
      const [, name, pid] = m;
      switch (loc) {
        case "en-US":
          return `Workflow queued: ${name} (task ${pid})`;
        case "zh-TW":
          return `工作流程已進入佇列：${name}（任務 ${pid}）`;
        case "zh-HK":
          return `工作流已入隊：${name}（任務 ${pid}）`;
        case "ja-JP":
          return `ワークフローをキューに追加しました: ${name}（タスク ${pid}）`;
        case "ko-KR":
          return `워크플로 대기열 등록됨: ${name} (작업 ${pid})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^ComfyUI 执行失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateComfyErrorDetail(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `ComfyUI execution failed: ${detail}`;
        case "zh-TW":
          return `ComfyUI 執行失敗：${detail}`;
        case "zh-HK":
          return `ComfyUI 執行失敗：${detail}`;
        case "ja-JP":
          return `ComfyUI の実行に失敗しました: ${detail}`;
        case "ko-KR":
          return `ComfyUI 실행 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^节点 .+$/s,
    when: (text) => parseComfyNodeError(text) !== null,
    format: (m, loc) => translateComfyErrorDetail(m[0], loc),
  },
  {
    re: /^工作流推理完成，正在保存 (\d+) 个输出作品$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `Workflow inference completed; saving ${n} output file(s)`;
        case "zh-TW":
          return `工作流程推論完成，正在儲存 ${n} 個輸出作品`;
        case "zh-HK":
          return `工作流推理完成，正在儲存 ${n} 個輸出作品`;
        case "ja-JP":
          return `ワークフロー推論が完了しました。${n} 件の出力を保存しています`;
        case "ko-KR":
          return `워크플로 추론 완료, 출력 결과물 ${n}개 저장 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^保存作品 (\d+)\/(\d+) · (.+)$/,
    format: (m, loc) => {
      const [, idx, total, name] = m;
      switch (loc) {
        case "en-US":
          return `Saving output ${idx}/${total} · ${name}`;
        case "zh-TW":
          return `儲存作品 ${idx}/${total} · ${name}`;
        case "zh-HK":
          return `儲存作品 ${idx}/${total} · ${name}`;
        case "ja-JP":
          return `出力を保存中 ${idx}/${total} · ${name}`;
        case "ko-KR":
          return `결과물 저장 중 ${idx}/${total} · ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流执行完成，已保存 (\d+) 个作品到远端 GPU$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `Workflow completed; saved ${n} output(s) to remote GPU`;
        case "zh-TW":
          return `工作流程執行完成，已儲存 ${n} 個作品至遠端 GPU`;
        case "zh-HK":
          return `工作流執行完成，已儲存 ${n} 個作品到遠端 GPU`;
        case "ja-JP":
          return `ワークフロー完了。${n} 件の出力をリモート GPU に保存しました`;
        case "ko-KR":
          return `워크플로 실행 완료, 원격 GPU에 결과물 ${n}개 저장됨`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · 排队中（第 (\d+) 位）· 已用 (\d+) 秒$/,
    format: (m, loc) => {
      const [, name, pos, sec] = m;
      switch (loc) {
        case "en-US":
          return `${name} · Queued (#${pos}) · ${sec}s elapsed`;
        case "zh-TW":
          return `${name} · 排隊中（第 ${pos} 位）· 已用 ${sec} 秒`;
        case "zh-HK":
          return `${name} · 排隊中（第 ${pos} 位）· 已用 ${sec} 秒`;
        case "ja-JP":
          return `${name} · キュー待機中（${pos} 番目）· ${sec} 秒経過`;
        case "ko-KR":
          return `${name} · 대기 중 (${pos}번째) · ${sec}초 경과`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · 远端 GPU 执行中 · 已用 (\d+) 秒$/,
    format: (m, loc) => {
      const [, name, sec] = m;
      switch (loc) {
        case "en-US":
          return `${name} · Running on remote GPU · ${sec}s elapsed`;
        case "zh-TW":
          return `${name} · 遠端 GPU 執行中 · 已用 ${sec} 秒`;
        case "zh-HK":
          return `${name} · 遠端 GPU 執行中 · 已用 ${sec} 秒`;
        case "ja-JP":
          return `${name} · リモート GPU で実行中 · ${sec} 秒経過`;
        case "ko-KR":
          return `${name} · 원격 GPU에서 실행 중 · ${sec}초 경과`;
        default:
          return m[0];
      }
    },
  },

  // === server/download.py & server/runtime.py transfer progress ===
  {
    re: /^分块下载：(\d+) 块，每块最多 (\d+) MiB，并发上限 (\d+)；复用 ([\d.]+) MiB$/,
    format: (m, loc) => {
      const [, chunks, chunkMib, threads, reused] = m;
      switch (loc) {
        case "en-US":
          return `Chunked download: ${chunks} chunks (up to ${chunkMib} MiB each), max ${threads} concurrent; resumed ${reused} MiB`;
        case "zh-TW":
          return `分塊下載：${chunks} 塊，每塊最多 ${chunkMib} MiB，並行上限 ${threads}；重複使用 ${reused} MiB`;
        case "zh-HK":
          return `分塊下載：${chunks} 塊，每塊最多 ${chunkMib} MiB，並發上限 ${threads}；複用 ${reused} MiB`;
        case "ja-JP":
          return `分割ダウンロード: ${chunks} チャンク（各最大 ${chunkMib} MiB）、最大並列 ${threads}、再利用 ${reused} MiB`;
        case "ko-KR":
          return `분할 다운로드: ${chunks}개 청크 (각 최대 ${chunkMib} MiB), 최대 동시 ${threads}개, 재사용 ${reused} MiB`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^进度 ([\d.]+)% · ([\d.]+)\/([\d.]+) GiB · ([\d.]+) MiB\/s · (\d+) 个连接$/,
    format: (m, loc) => {
      const [, pct, cur, tot, spd, conns] = m;
      switch (loc) {
        case "en-US":
          return `${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · ${conns} connections`;
        case "zh-TW":
          return `進度 ${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · ${conns} 個連線`;
        case "zh-HK":
          return `進度 ${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · ${conns} 個連線`;
        case "ja-JP":
          return `進捗 ${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · ${conns} 接続`;
        case "ko-KR":
          return `진행률 ${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · ${conns}개 연결`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^进度 ([\d.]+)% · ([\d.]+)\/([\d.]+) GiB · ([\d.]+) MiB\/s · 单连接$/,
    format: (m, loc) => {
      const [, pct, cur, tot, spd] = m;
      switch (loc) {
        case "en-US":
          return `${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · single connection`;
        case "zh-TW":
          return `進度 ${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · 單連線`;
        case "zh-HK":
          return `進度 ${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · 單連線`;
        case "ja-JP":
          return `進捗 ${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · 単一接続`;
        case "ko-KR":
          return `진행률 ${pct}% · ${cur}/${tot} GiB · ${spd} MiB/s · 단일 연결`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^分块 (\d+)\/(\d+) 下载失败（(.+?)），已保留完成的分块，可重试续传$/,
    format: (m, loc) => {
      const [, idx, tot, err] = m;
      switch (loc) {
        case "en-US":
          return `Chunk ${idx}/${tot} failed (${err}); completed chunks were preserved for resume`;
        case "zh-TW":
          return `分塊 ${idx}/${tot} 下載失敗（${err}），已保留完成的分塊，可重試續傳`;
        case "zh-HK":
          return `分塊 ${idx}/${tot} 下載失敗（${err}），已保留完成的分塊，可重試續傳`;
        case "ja-JP":
          return `チャンク ${idx}/${tot} のダウンロードに失敗しました（${err}）。完了済みチャンクを保持しているため再開可能です`;
        case "ko-KR":
          return `청크 ${idx}/${tot} 다운로드 실패 (${err}). 완료된 청크가 유지되어 이어받기가 가능합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^分块 (\d+)\/(\d+) 重试 (\d+)\/(\d+)（(.+?)）$/,
    format: (m, loc) => {
      const [, idx, tot, cur, max, err] = m;
      switch (loc) {
        case "en-US":
          return `Retrying chunk ${idx}/${tot} (${cur}/${max}, ${err})`;
        case "zh-TW":
          return `分塊 ${idx}/${tot} 重試 ${cur}/${max}（${err}）`;
        case "zh-HK":
          return `分塊 ${idx}/${tot} 重試 ${cur}/${max}（${err}）`;
        case "ja-JP":
          return `チャンク ${idx}/${tot} を再試行中 ${cur}/${max}（${err}）`;
        case "ko-KR":
          return `청크 ${idx}/${tot} 재시도 ${cur}/${max} (${err})`;
        default:
          return m[0];
      }
    },
  },

  // === server/drive.py & console/internal/drivemount/drivemount.go ===
  {
    re: /^Google 云盘连接失败：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Google Drive connection failed: ${detail}`;
        case "zh-TW":
          return `Google 雲端硬碟連線失敗：${detail}`;
        case "zh-HK":
          return `Google 雲端硬碟連接失敗：${detail}`;
        case "ja-JP":
          return `Google ドライブ接続に失敗しました: ${detail}`;
        case "ko-KR":
          return `Google 드라이브 연결 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google 云盘目录不可读写：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Google Drive folder is not readable/writable: ${detail}`;
        case "zh-TW":
          return `Google 雲端硬碟目錄無法讀寫：${detail}`;
        case "zh-HK":
          return `Google 雲端硬碟目錄無法讀寫：${detail}`;
        case "ja-JP":
          return `Google ドライブフォルダの読み書きができません: ${detail}`;
        case "ko-KR":
          return `Google 드라이브 폴더를 읽고 쓸 수 없습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已自动初始化免费 Drive 配额项目 \(([^)]+)\)，后续所有实例免弹窗自动挂载$/,
    format: (m, loc) => {
      const pid = m[1];
      switch (loc) {
        case "en-US":
          return `Initialized free Drive quota project (${pid}); future instances will mount automatically without prompts`;
        case "zh-TW":
          return `已自動初始化免費 Drive 配額專案 (${pid})，後續所有執行個體免彈窗自動掛載`;
        case "zh-HK":
          return `已自動初始化免費 Drive 配額項目 (${pid})，後續所有實例免彈窗自動掛載`;
        case "ja-JP":
          return `無料の Drive クォータプロジェクト (${pid}) を初期化しました。以降のインスタンスは確認なしで自動マウントされます`;
        case "ko-KR":
          return `무료 Drive 할당량 프로젝트(${pid})를 초기화했습니다. 이후 모든 인스턴스에서 팝업 없이 자동 마운트됩니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已确认免弹窗云盘配额项目 \(([^)]+)\)，后续所有实例免弹窗自动挂载$/,
    format: (m, loc) => {
      const pid = m[1];
      switch (loc) {
        case "en-US":
          return `Verified zero-prompt Drive quota project (${pid}); future instances will mount automatically without prompts`;
        case "zh-TW":
          return `已確認免彈窗雲端硬碟配額專案 (${pid})，後續所有執行個體免彈窗自動掛載`;
        case "zh-HK":
          return `已確認免彈窗雲端硬碟配額項目 (${pid})，後續所有實例免彈窗自動掛載`;
        case "ja-JP":
          return `確認不要ドライブ用クォータプロジェクト (${pid}) を確認しました。以降のインスタンスは確認なしで自動マウントされます`;
        case "ko-KR":
          return `무팝업 드라이브 할당량 프로젝트(${pid})를 확인했습니다. 이후 모든 인스턴스에서 팝업 없이 자동 마운트됩니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 原生云盘挂载连接中断，请重试：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Colab native Drive mount connection interrupted; please retry: ${detail}`;
        case "zh-TW":
          return `Google Colab 原生雲端硬碟掛載連線中斷，請重試：${detail}`;
        case "zh-HK":
          return `Google Colab 原生雲端硬碟掛載連接中斷，請重試：${detail}`;
        case "ja-JP":
          return `Google Colab ネイティブドライブのマウント接続が中断されました。再試行してください: ${detail}`;
        case "ko-KR":
          return `Google Colab 네이티브 드라이브 마운트 연결이 중단되었습니다. 다시 시도하세요: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 原生云盘挂载失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Colab native Drive mount failed: ${detail}`;
        case "zh-TW":
          return `Google Colab 原生雲端硬碟掛載失敗：${detail}`;
        case "zh-HK":
          return `Google Colab 原生雲端硬碟掛載失敗：${detail}`;
        case "ja-JP":
          return `Google Colab ネイティブドライブのマウントに失敗しました: ${detail}`;
        case "ko-KR":
          return `Google Colab 네이티브 드라이브 마운트 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google Colab 云盘授权接口返回异常状态（HTTP (\d+)），请重试$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Google Colab Drive authorization endpoint returned unexpected status (HTTP ${code}); please retry`;
        case "zh-TW":
          return `Google Colab 雲端硬碟授權介面回傳異常狀態（HTTP ${code}），請重試`;
        case "zh-HK":
          return `Google Colab 雲端硬碟授權介面返回異常狀態（HTTP ${code}），請重試`;
        case "ja-JP":
          return `Google Colab ドライブ認可エンドポイントが異常なステータス（HTTP ${code}）を返しました。再試行してください`;
        case "ko-KR":
          return `Google Colab 드라이브 인증 엔드포인트가 비정상 상태(HTTP ${code})를 반환했습니다. 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^未取得 Google Colab 云盘连接许可（HTTP (\d+)），请重试$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Did not receive Google Colab Drive connection permission (HTTP ${code}); please retry`;
        case "zh-TW":
          return `未取得 Google Colab 雲端硬碟連線許可（HTTP ${code}），請重試`;
        case "zh-HK":
          return `未取得 Google Colab 雲端硬碟連接許可（HTTP ${code}），請重試`;
        case "ja-JP":
          return `Google Colab ドライブ接続許可を取得できませんでした（HTTP ${code}）。再試行してください`;
        case "ko-KR":
          return `Google Colab 드라이브 연결 권한을 받지 못했습니다 (HTTP ${code}). 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^检查 Google 云盘授权状态失败（HTTP (\d+)），请重试$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Failed to check Google Drive authorization status (HTTP ${code}); please retry`;
        case "zh-TW":
          return `檢查 Google 雲端硬碟授權狀態失敗（HTTP ${code}），請重試`;
        case "zh-HK":
          return `檢查 Google 雲端硬碟授權狀態失敗（HTTP ${code}），請重試`;
        case "ja-JP":
          return `Google ドライブの認可状態確認に失敗しました（HTTP ${code}）。再試行してください`;
        case "ko-KR":
          return `Google 드라이브 인증 상태 확인에 실패했습니다 (HTTP ${code}). 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google 云盘授权确认失败（HTTP (\d+)），请重试$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Google Drive authorization confirmation failed (HTTP ${code}); please retry`;
        case "zh-TW":
          return `Google 雲端硬碟授權確認失敗（HTTP ${code}），請重試`;
        case "zh-HK":
          return `Google 雲端硬碟授權確認失敗（HTTP ${code}），請重試`;
        case "ja-JP":
          return `Google ドライブの認可確認に失敗しました（HTTP ${code}）。再試行してください`;
        case "ko-KR":
          return `Google 드라이브 인증 확인에 실패했습니다 (HTTP ${code}). 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Google 云盘连接返回 HTTP (\d+)，请重试$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Google Drive connection returned HTTP ${code}; please retry`;
        case "zh-TW":
          return `Google 雲端硬碟連線回傳 HTTP ${code}，請重試`;
        case "zh-HK":
          return `Google 雲端硬碟連接返回 HTTP ${code}，請重試`;
        case "ja-JP":
          return `Google ドライブ接続が HTTP ${code} を返しました。再試行してください`;
        case "ko-KR":
          return `Google 드라이브 연결이 HTTP ${code}을(를) 반환했습니다. 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^向远端实例同步云盘凭据失败（HTTP (\d+)），请重试$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Failed to propagate Drive credentials to the remote instance (HTTP ${code}); please retry`;
        case "zh-TW":
          return `向遠端執行個體同步雲端硬碟憑證失敗（HTTP ${code}），請重試`;
        case "zh-HK":
          return `向遠端實例同步雲端硬碟憑證失敗（HTTP ${code}），請重試`;
        case "ja-JP":
          return `リモートインスタンスへのドライブ認証情報同期に失敗しました（HTTP ${code}）。再試行してください`;
        case "ko-KR":
          return `원격 인스턴스로 드라이브 자격 증명 동기화에 실패했습니다 (HTTP ${code}). 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },

  // === server/openai_compat.py ===
  {
    re: /^(\S+) 必须为数字$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `${name} must be a number`;
        case "zh-TW":
          return `${name} 必須為數字`;
        case "zh-HK":
          return `${name} 必須為數字`;
        case "ja-JP":
          return `${name} は数値である必要があります`;
        case "ko-KR":
          return `${name}은(는) 숫자여야 합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(\S+) 不能为 NaN 或无穷大$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `${name} cannot be NaN or infinity`;
        case "zh-TW":
          return `${name} 不可為 NaN 或無窮大`;
        case "zh-HK":
          return `${name} 不能為 NaN 或無窮大`;
        case "ja-JP":
          return `${name} に NaN や無限大は指定できません`;
        case "ko-KR":
          return `${name}은(는) NaN 또는 무한대일 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(\S+) 超出允许范围$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `${name} is out of allowed range`;
        case "zh-TW":
          return `${name} 超出允許範圍`;
        case "zh-HK":
          return `${name} 超出允許範圍`;
        case "ja-JP":
          return `${name} が許容範囲を超えています`;
        case "ko-KR":
          return `${name}이(가) 허용 범위를 벗어났습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(\S+) 必须为整数$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `${name} must be an integer`;
        case "zh-TW":
          return `${name} 必須為整數`;
        case "zh-HK":
          return `${name} 必須為整數`;
        case "ja-JP":
          return `${name} は整数である必要があります`;
        case "ko-KR":
          return `${name}은(는) 정수여야 합니다`;
        default:
          return m[0];
      }
    },
  },

  // === server/public_api.py ===
  {
    re: /^API 请求 #(\d+) 完成((?: · .+)+)$/,
    format: (m, loc) => {
      const seq = m[1];
      const parts = m[2]!
        .split(" · ")
        .filter(Boolean)
        .map((p) => translateApiFinishPart(p, loc))
        .join(" · ");
      switch (loc) {
        case "en-US":
          return `API request #${seq} completed · ${parts}`;
        case "zh-TW":
          return `API 請求 #${seq} 完成 · ${parts}`;
        case "zh-HK":
          return `API 請求 #${seq} 完成 · ${parts}`;
        case "ja-JP":
          return `API リクエスト #${seq} 完了 · ${parts}`;
        case "ko-KR":
          return `API 요청 #${seq} 완료 · ${parts}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^API 请求 #(\d+) 已取消 · 耗时 (.+)$/,
    format: (m, loc) => {
      const [, seq, dur] = m;
      switch (loc) {
        case "en-US":
          return `API request #${seq} cancelled · elapsed ${dur}`;
        case "zh-TW":
          return `API 請求 #${seq} 已取消 · 耗時 ${dur}`;
        case "zh-HK":
          return `API 請求 #${seq} 已取消 · 耗時 ${dur}`;
        case "ja-JP":
          return `API リクエスト #${seq} キャンセル済み · 所要 ${dur}`;
        case "ko-KR":
          return `API 요청 #${seq} 취소됨 · 소요 ${dur}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^API 请求 #(\d+) 失败 · 耗时 ([^\s·]+)(?: · (.+))?$/,
    format: (m, loc) => {
      const [, seq, dur, errRaw] = m;
      const errPart = errRaw ? ` · ${translateInner(errRaw, loc)}` : "";
      switch (loc) {
        case "en-US":
          return `API request #${seq} failed · elapsed ${dur}${errPart}`;
        case "zh-TW":
          return `API 請求 #${seq} 失敗 · 耗時 ${dur}${errPart}`;
        case "zh-HK":
          return `API 請求 #${seq} 失敗 · 耗時 ${dur}${errPart}`;
        case "ja-JP":
          return `API リクエスト #${seq} 失敗 · 所要 ${dur}${errPart}`;
        case "ko-KR":
          return `API 요청 #${seq} 실패 · 소요 ${dur}${errPart}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已更新模型 API ID：(.+)$/,
    format: (m, loc) => {
      const apiId = m[1];
      switch (loc) {
        case "en-US":
          return `Updated Model API ID: ${apiId}`;
        case "zh-TW":
          return `已更新模型 API ID：${apiId}`;
        case "zh-HK":
          return `已更新模型 API ID：${apiId}`;
        case "ja-JP":
          return `モデル API ID を更新しました: ${apiId}`;
        case "ko-KR":
          return `모델 API ID를 업데이트했습니다: ${apiId}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^API 请求 #(\d+) · (\S+) · 模型 (.+)$/,
    format: (m, loc) => {
      const [, seq, kind, modelId] = m;
      switch (loc) {
        case "en-US":
          return `API request #${seq} · ${kind} · model ${modelId}`;
        case "zh-TW":
          return `API 請求 #${seq} · ${kind} · 模型 ${modelId}`;
        case "zh-HK":
          return `API 請求 #${seq} · ${kind} · 模型 ${modelId}`;
        case "ja-JP":
          return `API リクエスト #${seq} · ${kind} · モデル ${modelId}`;
        case "ko-KR":
          return `API 요청 #${seq} · ${kind} · 모델 ${modelId}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^推理引擎返回 HTTP (\d+)$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Inference engine returned HTTP ${code}`;
        case "zh-TW":
          return `推論引擎回傳 HTTP ${code}`;
        case "zh-HK":
          return `推理引擎返回 HTTP ${code}`;
        case "ja-JP":
          return `推論エンジンが HTTP ${code} を返しました`;
        case "ko-KR":
          return `추론 엔진이 HTTP ${code}을(를) 반환했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^输入上下文过长（(\d+) tokens），超过当前模型上下文上限（(\d+) tokens）$/,
    format: (m, loc) => {
      const [, promptTok, ctxTok] = m;
      switch (loc) {
        case "en-US":
          return `Input context (${promptTok} tokens) exceeds the active model context limit (${ctxTok} tokens)`;
        case "zh-TW":
          return `輸入上下文過長（${promptTok} tokens），超過目前模型上下文上限（${ctxTok} tokens）`;
        case "zh-HK":
          return `輸入上下文過長（${promptTok} tokens），超過目前模型上下文上限（${ctxTok} tokens）`;
        case "ja-JP":
          return `入力コンテキスト（${promptTok} tokens）が現在のモデル上限（${ctxTok} tokens）を超えています`;
        case "ko-KR":
          return `입력 컨텍스트(${promptTok} tokens)가 현재 모델 컨텍스트 상한(${ctxTok} tokens)을 초과했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^输入上下文超过当前模型上限（(\d+) tokens）$/,
    format: (m, loc) => {
      const ctxTok = m[1];
      switch (loc) {
        case "en-US":
          return `Input context exceeds the active model context limit (${ctxTok} tokens)`;
        case "zh-TW":
          return `輸入上下文超過目前模型上限（${ctxTok} tokens）`;
        case "zh-HK":
          return `輸入上下文超過目前模型上限（${ctxTok} tokens）`;
        case "ja-JP":
          return `入力コンテキストが現在のモデル上限（${ctxTok} tokens）を超えています`;
        case "ko-KR":
          return `입력 컨텍스트가 현재 모델 컨텍스트 상한(${ctxTok} tokens)을 초과했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^API 请求(?: #(\d+) )?上下文超限（约 (\d+) \/ (\d+) tokens），已自动压缩为约 (\d+) tokens$/,
    format: (m, loc) => {
      const [, seq, origTok, ctxTok, afterTok] = m;
      const reqPrefix = seq ? ` #${seq}` : "";
      switch (loc) {
        case "en-US":
          return `API request${reqPrefix} exceeded context limit (~${origTok} / ${ctxTok} tokens); auto-compacted to ~${afterTok} tokens`;
        case "zh-TW":
          return `API 請求${reqPrefix} 上下文超限（約 ${origTok} / ${ctxTok} tokens），已自動壓縮為約 ${afterTok} tokens`;
        case "zh-HK":
          return `API 請求${reqPrefix} 上下文超限（約 ${origTok} / ${ctxTok} tokens），已自動壓縮為約 ${afterTok} tokens`;
        case "ja-JP":
          return `API リクエスト${reqPrefix} のコンテキスト上限超過（約 ${origTok} / ${ctxTok} tokens）のため約 ${afterTok} tokens に自動圧縮しました`;
        case "ko-KR":
          return `API 요청${reqPrefix} 컨텍스트 상한 초과(약 ${origTok} / ${ctxTok} tokens)로 약 ${afterTok} tokens로 자동 압축했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^请求体最大 (\d+) MB$/,
    format: (m, loc) => {
      const mb = m[1];
      switch (loc) {
        case "en-US":
          return `Maximum request body size is ${mb} MB`;
        case "zh-TW":
          return `請求內容最大 ${mb} MB`;
        case "zh-HK":
          return `請求主體最大 ${mb} MB`;
        case "ja-JP":
          return `リクエストボディの上限は ${mb} MB です`;
        case "ko-KR":
          return `요청 본문 크기는 최대 ${mb} MB입니다`;
        default:
          return m[0];
      }
    },
  },

  // === server/runtime.py ===
  {
    re: /^下载失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Download failed: ${detail}`;
        case "zh-TW":
          return `下載失敗：${detail}`;
        case "zh-HK":
          return `下載失敗：${detail}`;
        case "ja-JP":
          return `ダウンロードに失敗しました: ${detail}`;
        case "ko-KR":
          return `다운로드 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^准备下载 (.+?)：(\d+) 个文件，共 ([\d.]+) GiB，(\d+) 线程，目标为远端 GPU 磁盘$/,
    format: (m, loc) => {
      const [, name, count, gib, threads] = m;
      switch (loc) {
        case "en-US":
          return `Preparing to download ${name}: ${count} file(s), ${gib} GiB total, ${threads} threads -> remote GPU disk`;
        case "zh-TW":
          return `準備下載 ${name}：${count} 個檔案，共 ${gib} GiB，${threads} 執行緒，目標為遠端 GPU 磁碟`;
        case "zh-HK":
          return `準備下載 ${name}：${count} 個檔案，共 ${gib} GiB，${threads} 線程，目標為遠端 GPU 磁碟`;
        case "ja-JP":
          return `${name} のダウンロードを準備中: ${count} ファイル、合計 ${gib} GiB、${threads} スレッド（保存先: リモート GPU ディスク）`;
        case "ko-KR":
          return `${name} 다운로드 준비 중: ${count}개 파일, 총 ${gib} GiB, ${threads}스레드 (대상: 원격 GPU 디스크)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端可用磁盘 ([\d.]+) GiB，本次预计新增 ([\d.]+) GiB$/,
    format: (m, loc) => {
      const [, free, add] = m;
      switch (loc) {
        case "en-US":
          return `Remote free disk: ${free} GiB; estimated new space needed: ${add} GiB`;
        case "zh-TW":
          return `遠端可用磁碟 ${free} GiB，本次預計新增 ${add} GiB`;
        case "zh-HK":
          return `遠端可用磁碟 ${free} GiB，本次預計新增 ${add} GiB`;
        case "ja-JP":
          return `リモート空き容量 ${free} GiB、今回の追加予定 ${add} GiB`;
        case "ko-KR":
          return `원격 사용 가능 디스크 ${free} GiB, 이번 예상 추가 용량 ${add} GiB`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^文件 (\d+)\/(\d+) 已存在且校验通过，跳过：(.+)$/,
    format: (m, loc) => {
      const [, idx, tot, path] = m;
      switch (loc) {
        case "en-US":
          return `File ${idx}/${tot} already exists and verified; skipping: ${path}`;
        case "zh-TW":
          return `檔案 ${idx}/${tot} 已存在且驗證通過，略過：${path}`;
        case "zh-HK":
          return `檔案 ${idx}/${tot} 已存在且校驗通過，跳過：${path}`;
        case "ja-JP":
          return `ファイル ${idx}/${tot} は検証済みのためスキップします: ${path}`;
        case "ko-KR":
          return `파일 ${idx}/${tot} 이미 존재하며 검증 완료되어 건너뜀: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^文件 (\d+)\/(\d+) · (.+?) · ([\d.]+) GiB$/,
    format: (m, loc) => {
      const [, idx, tot, path, gib] = m;
      switch (loc) {
        case "en-US":
          return `File ${idx}/${tot} · ${path} · ${gib} GiB`;
        case "zh-TW":
          return `檔案 ${idx}/${tot} · ${path} · ${gib} GiB`;
        case "zh-HK":
          return `檔案 ${idx}/${tot} · ${path} · ${gib} GiB`;
        case "ja-JP":
          return `ファイル ${idx}/${tot} · ${path} · ${gib} GiB`;
        case "ko-KR":
          return `파일 ${idx}/${tot} · ${path} · ${gib} GiB`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^校验 SHA-256 · (.+)$/,
    format: (m, loc) => {
      const path = m[1];
      switch (loc) {
        case "en-US":
          return `Verifying SHA-256 · ${path}`;
        case "zh-TW":
          return `驗證 SHA-256 · ${path}`;
        case "zh-HK":
          return `校驗 SHA-256 · ${path}`;
        case "ja-JP":
          return `SHA-256 を検証中 · ${path}`;
        case "ko-KR":
          return `SHA-256 검증 중 · ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^文件 (\d+)\/(\d+) 传输完成，正在校验 SHA-256$/,
    format: (m, loc) => {
      const [, idx, tot] = m;
      switch (loc) {
        case "en-US":
          return `File ${idx}/${tot} downloaded; verifying SHA-256`;
        case "zh-TW":
          return `檔案 ${idx}/${tot} 傳輸完成，正在驗證 SHA-256`;
        case "zh-HK":
          return `檔案 ${idx}/${tot} 傳輸完成，正在校驗 SHA-256`;
        case "ja-JP":
          return `ファイル ${idx}/${tot} の転送完了。SHA-256 を検証しています`;
        case "ko-KR":
          return `파일 ${idx}/${tot} 전송 완료, SHA-256 검증 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^文件 (\d+)\/(\d+) 校验通过，已保存到远端$/,
    format: (m, loc) => {
      const [, idx, tot] = m;
      switch (loc) {
        case "en-US":
          return `File ${idx}/${tot} verified and saved remotely`;
        case "zh-TW":
          return `檔案 ${idx}/${tot} 驗證通過，已儲存至遠端`;
        case "zh-HK":
          return `檔案 ${idx}/${tot} 校驗通過，已儲存至遠端`;
        case "ja-JP":
          return `ファイル ${idx}/${tot} の検証に合格しリモートに保存しました`;
        case "ko-KR":
          return `파일 ${idx}/${tot} 검증 통과 및 원격 저장 완료`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^全部文件已验证：([\d.]+) GiB，用时 ([\d.]+) 秒，平均传输 ([\d.]+) MiB\/s；复用 ([\d.]+) MiB$/,
    format: (m, loc) => {
      const [, gib, sec, spd, reused] = m;
      switch (loc) {
        case "en-US":
          return `All files verified: ${gib} GiB in ${sec}s (avg ${spd} MiB/s; resumed ${reused} MiB)`;
        case "zh-TW":
          return `所有檔案已驗證：${gib} GiB，耗時 ${sec} 秒，平均傳輸 ${spd} MiB/s；重複使用 ${reused} MiB`;
        case "zh-HK":
          return `全部檔案已驗證：${gib} GiB，用時 ${sec} 秒，平均傳輸 ${spd} MiB/s；複用 ${reused} MiB`;
        case "ja-JP":
          return `全ファイル検証完了: ${gib} GiB、所要 ${sec} 秒、平均 ${spd} MiB/s（再利用 ${reused} MiB）`;
        case "ko-KR":
          return `모든 파일 검증 완료: ${gib} GiB, ${sec}초 소요, 평균 ${spd} MiB/s (재사용 ${reused} MiB)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^首次加载，准备远端 CUDA 推理引擎 (.+)$/,
    format: (m, loc) => {
      const tag = m[1];
      switch (loc) {
        case "en-US":
          return `First load: preparing remote CUDA inference engine ${tag}`;
        case "zh-TW":
          return `首次載入，準備遠端 CUDA 推論引擎 ${tag}`;
        case "zh-HK":
          return `首次載入，準備遠端 CUDA 推理引擎 ${tag}`;
        case "ja-JP":
          return `初回ロードのためリモート CUDA 推論エンジン ${tag} を準備しています`;
        case "ko-KR":
          return `최초 로드: 원격 CUDA 추론 엔진 ${tag} 준비 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^下载引擎组件 (\d+)\/(\d+)$/,
    format: (m, loc) => {
      const [, idx, tot] = m;
      switch (loc) {
        case "en-US":
          return `Downloading engine asset ${idx}/${tot}`;
        case "zh-TW":
          return `下載引擎元件 ${idx}/${tot}`;
        case "zh-HK":
          return `下載引擎組件 ${idx}/${tot}`;
        case "ja-JP":
          return `エンジンコンポーネントをダウンロード中 ${idx}/${tot}`;
        case "ko-KR":
          return `엔진 구성 요소 다운로드 중 ${idx}/${tot}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^引擎组件 (\d+) 校验通过，正在解压$/,
    format: (m, loc) => {
      const idx = m[1];
      switch (loc) {
        case "en-US":
          return `Engine asset ${idx} verified; extracting`;
        case "zh-TW":
          return `引擎元件 ${idx} 驗證通過，正在解壓縮`;
        case "zh-HK":
          return `引擎組件 ${idx} 校驗通過，正在解壓`;
        case "ja-JP":
          return `エンジンコンポーネント ${idx} の検証完了。展開しています`;
        case "ko-KR":
          return `엔진 구성 요소 ${idx} 검증 완료, 압축 해제 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端设备 (.+?)，显存 ([\d.]+) GiB(?:，模型权重 ([\d.]+) GiB)?$/,
    format: (m, loc) => {
      const gpuName = translateInner(m[1]!, loc);
      const vram = m[2];
      const weight = m[3];
      switch (loc) {
        case "en-US":
          return weight
            ? `Remote GPU ${gpuName}, ${vram} GiB VRAM, model weights ${weight} GiB`
            : `Remote GPU ${gpuName}, ${vram} GiB VRAM`;
        case "zh-TW":
          return weight
            ? `遠端裝置 ${gpuName}，VRAM ${vram} GiB，模型權重 ${weight} GiB`
            : `遠端裝置 ${gpuName}，VRAM ${vram} GiB`;
        case "zh-HK":
          return weight
            ? `遠端裝置 ${gpuName}，顯存 ${vram} GiB，模型權重 ${weight} GiB`
            : `遠端裝置 ${gpuName}，顯存 ${vram} GiB`;
        case "ja-JP":
          return weight
            ? `リモートデバイス ${gpuName}、VRAM ${vram} GiB、モデルウェイト ${weight} GiB`
            : `リモートデバイス ${gpuName}、VRAM ${vram} GiB`;
        case "ko-KR":
          return weight
            ? `원격 장치 ${gpuName}, VRAM ${vram} GiB, 모델 가중치 ${weight} GiB`
            : `원격 장치 ${gpuName}, VRAM ${vram} GiB`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型需要至少 (\d+) MiB 显存$/,
    format: (m, loc) => {
      const mib = m[1];
      switch (loc) {
        case "en-US":
          return `Model requires at least ${mib} MiB of VRAM`;
        case "zh-TW":
          return `模型至少需要 ${mib} MiB VRAM`;
        case "zh-HK":
          return `模型最少需要 ${mib} MiB 顯存`;
        case "ja-JP":
          return `このモデルには最低 ${mib} MiB の VRAM が必要です`;
        case "ko-KR":
          return `이 모델은 최소 ${mib} MiB의 VRAM이 필요합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型加载失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Model load failed: ${detail}`;
        case "zh-TW":
          return `模型載入失敗：${detail}`;
        case "zh-HK":
          return `模型載入失敗：${detail}`;
        case "ja-JP":
          return `モデルのロードに失敗しました: ${detail}`;
        case "ko-KR":
          return `모델 로드 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型预热失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Model warmup failed: ${detail}`;
        case "zh-TW":
          return `模型預熱失敗：${detail}`;
        case "zh-HK":
          return `模型預熱失敗：${detail}`;
        case "ja-JP":
          return `モデルのウォームアップに失敗しました: ${detail}`;
        case "ko-KR":
          return `모델 워밍업 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^启动 llama\.cpp：(.+)$/,
    format: (m, loc) => {
      const plan = translateLoadPlanLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Starting llama.cpp: ${plan}`;
        case "zh-TW":
          return `啟動 llama.cpp：${plan}`;
        case "zh-HK":
          return `啟動 llama.cpp：${plan}`;
        case "ja-JP":
          return `llama.cpp を起動: ${plan}`;
        case "ko-KR":
          return `llama.cpp 시작: ${plan}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型权重已就绪（(.+?)，耗时 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const plan = translateLoadPlanLabel(m[1]!, loc);
      const sec = m[2];
      switch (loc) {
        case "en-US":
          return `Model weights ready (${plan}, ${sec}s)`;
        case "zh-TW":
          return `模型權重已就緒（${plan}，耗時 ${sec} 秒）`;
        case "zh-HK":
          return `模型權重已就緒（${plan}，耗時 ${sec} 秒）`;
        case "ja-JP":
          return `モデルウェイト準備完了（${plan}、${sec} 秒）`;
        case "ko-KR":
          return `모델 가중치 준비 완료 (${plan}, ${sec}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^方案「(.+)」未启动成功，自动尝试下一降级装载方案…$/,
    format: (m, loc) => {
      const plan = translateLoadPlanLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Plan '${plan}' did not start cleanly; trying next fallback plan…`;
        case "zh-TW":
          return `方案「${plan}」未啟動成功，自動嘗試下一個降級載入方案…`;
        case "zh-HK":
          return `方案「${plan}」未啟動成功，自動嘗試下一個降級載入方案…`;
        case "ja-JP":
          return `プラン「${plan}」の起動に失敗しました。次のフォールバック案を試行します…`;
        case "ko-KR":
          return `'${plan}' 방안이 시작되지 않아 다음 폴백 로드 방안을 시도합니다…`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^方案「(.+?)」未启动成功（(.+)），自动尝试下一降级装载方案…$/s,
    format: (m, loc) => {
      const plan = translateLoadPlanLabel(m[1]!, loc);
      const detail = translateInner(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `Plan '${plan}' failed (${detail}); trying next fallback plan…`;
        case "zh-TW":
          return `方案「${plan}」未啟動成功（${detail}），自動嘗試下一個降級載入方案…`;
        case "zh-HK":
          return `方案「${plan}」未啟動成功（${detail}），自動嘗試下一個降級載入方案…`;
        case "ja-JP":
          return `プラン「${plan}」の起動に失敗しました（${detail}）。次のフォールバック案を試行します…`;
        case "ko-KR":
          return `'${plan}' 방안이 시작되지 않아(${detail}) 다음 폴백 로드 방안을 시도합니다…`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前显存紧张，上下文已自动调整为 (\d+) tokens 以完成装载$/,
    format: (m, loc) => {
      const ctx = m[1];
      switch (loc) {
        case "en-US":
          return `VRAM is tight; context was automatically adjusted to ${ctx} tokens to complete loading`;
        case "zh-TW":
          return `目前 VRAM 吃緊，上下文已自動調整為 ${ctx} tokens 以完成載入`;
        case "zh-HK":
          return `目前顯存緊張，上下文已自動調整為 ${ctx} tokens 以完成載入`;
        case "ja-JP":
          return `VRAM が逼迫しているため、ロード完了のためコンテキストを ${ctx} tokens に自動調整しました`;
        case "ko-KR":
          return `VRAM이 부족하여 로드를 완료하기 위해 컨텍스트를 ${ctx} tokens로 자동 조정했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^推理进程被系统终止（可能因系统内存不足 OOM）(?:：(.+))?$/s,
    format: (m, loc) => {
      const detail = m[1] ? translateInner(m[1], loc) : "";
      switch (loc) {
        case "en-US":
          return detail
            ? `Inference process was killed by OS (likely host RAM OOM): ${detail}`
            : "Inference process was killed by OS (likely host RAM OOM)";
        case "zh-TW":
          return detail
            ? `推論處理程序遭系統終止（可能因系統記憶體不足 OOM）：${detail}`
            : "推論處理程序遭系統終止（可能因系統記憶體不足 OOM）";
        case "zh-HK":
          return detail
            ? `推理進程被系統終止（可能因系統記憶體不足 OOM）：${detail}`
            : "推理進程被系統終止（可能因系統記憶體不足 OOM）";
        case "ja-JP":
          return detail
            ? `推論プロセスが OS により強制終了されました（ホスト RAM 不足 OOM の可能性）: ${detail}`
            : "推論プロセスが OS により強制終了されました（ホスト RAM 不足 OOM の可能性）";
        case "ko-KR":
          return detail
            ? `추론 프로세스가 OS에 의해 종료되었습니다(시스템 RAM 부족 OOM 가능성): ${detail}`
            : "추론 프로세스가 OS에 의해 종료되었습니다(시스템 RAM 부족 OOM 가능성)";
        default:
          return m[0];
      }
    },
  },
  {
    re: /^准备加载 (.+)，上下文 (\d+) tokens$/,
    format: (m, loc) => {
      const [, name, ctx] = m;
      switch (loc) {
        case "en-US":
          return `Preparing to load ${name} (${ctx} token context)`;
        case "zh-TW":
          return `準備載入 ${name}，上下文 ${ctx} tokens`;
        case "zh-HK":
          return `準備載入 ${name}，上下文 ${ctx} tokens`;
        case "ja-JP":
          return `${name} のロードを準備中（コンテキスト ${ctx} tokens）`;
        case "ko-KR":
          return `${name} 로드 준비 중 (컨텍스트 ${ctx} tokens)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在卸载当前模型 (.+) 以释放显存$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Unloading current model ${name} to free VRAM`;
        case "zh-TW":
          return `正在卸載目前模型 ${name} 以釋放 VRAM`;
        case "zh-HK":
          return `正在卸載目前模型 ${name} 以釋放顯存`;
        case "ja-JP":
          return `VRAM 解放のため現在のモデル ${name} をアンロードしています`;
        case "ko-KR":
          return `VRAM 확보를 위해 현재 모델 ${name} 언로드 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在重新装载 (.+)，先卸载当前(?:已装载模型|实例)以释放显存$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Reloading ${name}; unloading the currently loaded model first to free VRAM`;
        case "zh-TW":
          return `正在重新載入 ${name}，先卸載目前已載入的模型以釋放 VRAM`;
        case "zh-HK":
          return `正在重新載入 ${name}，先卸載目前已載入的模型以釋放顯存`;
        case "ja-JP":
          return `${name} を再ロード中（VRAM 解放のため現在のモデルをアンロードしています）`;
        case "ko-KR":
          return `${name} 다시 로드 중 (VRAM 확보를 위해 현재 로드된 모델 언로드)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在载入模型权重（已耗时 (\d+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Loading model weights (${sec}s elapsed)`;
        case "zh-TW":
          return `正在載入模型權重（已耗時 ${sec} 秒）`;
        case "zh-HK":
          return `正在載入模型權重（已耗時 ${sec} 秒）`;
        case "ja-JP":
          return `モデルウェイトを読み込んでいます（${sec} 秒経過）`;
        case "ko-KR":
          return `모델 가중치를 로드하는 중 (${sec}초 경과)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^CUDA 推理内核预热完成（耗时 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `CUDA inference kernel warmup finished (${sec}s)`;
        case "zh-TW":
          return `CUDA 推論核心預熱完成（耗時 ${sec} 秒）`;
        case "zh-HK":
          return `CUDA 推理核心預熱完成（耗時 ${sec} 秒）`;
        case "ja-JP":
          return `CUDA 推論カーネルのウォームアップが完了しました（${sec} 秒）`;
        case "ko-KR":
          return `CUDA 추론 커널 워밍업 완료 (${sec}초)`;
        default:
          return m[0];
      }
    },
  },

  // === server/tunnel.py ===
  {
    re: /^复用远端已校验的 Cloudflared (.+)$/,
    format: (m, loc) => {
      const tag = m[1];
      switch (loc) {
        case "en-US":
          return `Reusing verified remote Cloudflared ${tag}`;
        case "zh-TW":
          return `重複使用遠端已驗證的 Cloudflared ${tag}`;
        case "zh-HK":
          return `複用遠端已校驗的 Cloudflared ${tag}`;
        case "ja-JP":
          return `検証済みのリモート Cloudflared ${tag} を再利用します`;
        case "ko-KR":
          return `검증된 원격 Cloudflared ${tag} 재사용 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在远端下载 Cloudflared (.+)$/,
    format: (m, loc) => {
      const tag = m[1];
      switch (loc) {
        case "en-US":
          return `Downloading Cloudflared ${tag} on remote instance`;
        case "zh-TW":
          return `正在遠端下載 Cloudflared ${tag}`;
        case "zh-HK":
          return `正在遠端下載 Cloudflared ${tag}`;
        case "ja-JP":
          return `リモートで Cloudflared ${tag} をダウンロードしています`;
        case "ko-KR":
          return `원격에서 Cloudflared ${tag} 다운로드 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在等待公网域名路由生效并完成鉴权探测（已耗时 (\d+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Waiting for public domain routing and auth probe (${sec}s elapsed)`;
        case "zh-TW":
          return `正在等待公開網域路由生效並完成驗證探測（已耗時 ${sec} 秒）`;
        case "zh-HK":
          return `正在等待公網域名路由生效並完成鑑權探測（已耗時 ${sec} 秒）`;
        case "ja-JP":
          return `公開ドメインのルーティング反映と認証チェックを待機中（${sec} 秒経過）`;
        case "ko-KR":
          return `공용 도메인 라우팅 적용 및 인증 확인 대기 중 (${sec}초 경과)`;
        default:
          return m[0];
      }
    },
  },

  // === server/vm_boot.py ===
  {
    re: /^仍在安装远端服务依赖（已耗时 (\d+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Still installing remote service dependencies (${sec}s elapsed)`;
        case "zh-TW":
          return `仍在安裝遠端服務相依套件（已耗時 ${sec} 秒）`;
        case "zh-HK":
          return `仍在安裝遠端服務依賴（已耗時 ${sec} 秒）`;
        case "ja-JP":
          return `リモートサービス依存関係を引き続きインストール中（${sec} 秒経過）`;
        case "ko-KR":
          return `원격 서비스 종속성 설치 중 (${sec}초 경과)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端服务依赖安装失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Remote service dependency installation failed: ${detail}`;
        case "zh-TW":
          return `遠端服務相依套件安裝失敗：${detail}`;
        case "zh-HK":
          return `遠端服務依賴安裝失敗：${detail}`;
        case "ja-JP":
          return `リモートサービス依存関係のインストールに失敗しました: ${detail}`;
        case "ko-KR":
          return `원격 서비스 종속성 설치 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端服务依赖已就绪（耗时 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Remote service dependencies ready (${sec}s)`;
        case "zh-TW":
          return `遠端服務相依套件已就緒（耗時 ${sec} 秒）`;
        case "zh-HK":
          return `遠端服務依賴已就緒（耗時 ${sec} 秒）`;
        case "ja-JP":
          return `リモートサービス依存関係の準備が完了しました（${sec} 秒）`;
        case "ko-KR":
          return `원격 서비스 종속성 준비 완료 (${sec}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端管理服务健康检查通过（耗时 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Remote control service passed health check (${sec}s)`;
        case "zh-TW":
          return `遠端管理服務健康檢查通過（耗時 ${sec} 秒）`;
        case "zh-HK":
          return `遠端管理服務健康檢查通過（耗時 ${sec} 秒）`;
        case "ja-JP":
          return `リモート管理サービスのヘルスチェックに合格しました（${sec} 秒）`;
        case "ko-KR":
          return `원격 관리 서비스 상태 확인 통과 (${sec}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在等待远端管理服务就绪（已耗时 (\d+) 秒）$/,
    format: (m, loc) => {
      const sec = m[1];
      switch (loc) {
        case "en-US":
          return `Waiting for remote control service to become ready (${sec}s elapsed)`;
        case "zh-TW":
          return `正在等待遠端管理服務就緒（已耗時 ${sec} 秒）`;
        case "zh-HK":
          return `正在等待遠端管理服務就緒（已耗時 ${sec} 秒）`;
        case "ja-JP":
          return `リモート管理サービスの準備完了を待機中（${sec} 秒経過）`;
        case "ko-KR":
          return `원격 관리 서비스 준비 대기 중 (${sec}초 경과)`;
        default:
          return m[0];
      }
    },
  },

  // === common/comfyui.py ===
  {
    re: /^(.+?) · (反向提示词|正向提示词|文本输入|参考图片|输入音频|宽度|高度|帧数|帧率 \(FPS\)|批次大小|采样步数|CFG 引导系数|重绘幅度|随机种子)$/,
    format: (m, loc) => {
      const [, title, suffix] = m;
      const mapped = COMFY_FIELD_SUFFIX_LABELS[suffix!]?.[loc] ?? suffix;
      return `${title} · ${mapped}`;
    },
  },
  {
    re: /^查询 ComfyUI 任务进度连续失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Repeated failures querying ComfyUI job progress: ${detail}`;
        case "zh-TW":
          return `查詢 ComfyUI 任務進度連續失敗：${detail}`;
        case "zh-HK":
          return `查詢 ComfyUI 任務進度連續失敗：${detail}`;
        case "ja-JP":
          return `ComfyUI ジョブ進捗の取得に連続して失敗しました: ${detail}`;
        case "ko-KR":
          return `ComfyUI 작업 진행 상태 조회가 연속으로 실패했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^单连接下载重试 (\d+)\/(\d+)（(.+)）$/,
    format: (m, loc) => {
      const [, cur, total, reason] = m;
      switch (loc) {
        case "en-US":
          return `Single-stream download retry ${cur}/${total} (${reason})`;
        case "zh-TW":
          return `單連線下載重試 ${cur}/${total}（${reason}）`;
        case "zh-HK":
          return `單連接下載重試 ${cur}/${total}（${reason}）`;
        case "ja-JP":
          return `単一ストリームダウンロードを再試行中 ${cur}/${total}（${reason}）`;
        case "ko-KR":
          return `단일 연결 다운로드 재시도 ${cur}/${total} (${reason})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型(加载|预热)失败（推理进程已退出，退出码 ([^）]+)）(?:：(.+))?$/s,
    format: (m, loc) => {
      const [, stage, code, tailRaw] = m;
      const tail = tailRaw ? translateInner(tailRaw, loc) : "";
      const isWarmup = stage === "预热";
      switch (loc) {
        case "en-US":
          return (
            (isWarmup
              ? `Model warmup failed (inference process exited with code ${code})`
              : `Model load failed (inference process exited with code ${code})`) +
            (tail ? `: ${tail}` : "")
          );
        case "zh-TW":
          return (
            (isWarmup
              ? `模型預熱失敗（推論處理程序已結束，結束代碼 ${code}）`
              : `模型載入失敗（推論處理程序已結束，結束代碼 ${code}）`) +
            (tail ? `：${tail}` : "")
          );
        case "zh-HK":
          return (
            (isWarmup
              ? `模型預熱失敗（推理進程已退出，退出碼 ${code}）`
              : `模型載入失敗（推理進程已退出，退出碼 ${code}）`) +
            (tail ? `：${tail}` : "")
          );
        case "ja-JP":
          return (
            (isWarmup
              ? `モデルのウォームアップに失敗しました（推論プロセスが終了しました、終了コード ${code}）`
              : `モデルの読み込みに失敗しました（推論プロセスが終了しました、終了コード ${code}）`) +
            (tail ? `: ${tail}` : "")
          );
        case "ko-KR":
          return (
            (isWarmup
              ? `모델 워밍업 실패 (추론 프로세스 종료, 종료 코드 ${code})`
              : `모델 로드 실패 (추론 프로세스 종료, 종료 코드 ${code})`) +
            (tail ? `: ${tail}` : "")
          );
        default:
          return m[0];
      }
    },
  },
  {
    re: /^新服务未就绪，已恢复旧服务；模型文件与(?:运行配置|预算)保持不变（日志：(.+)）$/s,
    format: (m, loc) => {
      const tail = m[1]!;
      switch (loc) {
        case "en-US":
          return `New service did not become ready; restored the previous service while keeping model files and runtime config intact (log: ${tail})`;
        case "zh-TW":
          return `新服務尚未就緒，已復原舊服務；模型檔案與執行設定維持不變（日誌：${tail}）`;
        case "zh-HK":
          return `新服務未就緒，已恢復舊服務；模型檔案與運行配置保持不變（日誌：${tail}）`;
        case "ja-JP":
          return `新しいサービスが準備完了せず、旧サービスへロールバックしました。モデルと実行設定は保持されます（ログ: ${tail}）`;
        case "ko-KR":
          return `새 서비스가 준비되지 않아 이전 서비스로 복구했습니다. 모델 파일과 실행 설정은 유지됩니다 (로그: ${tail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^新服务未就绪且回滚后健康检查未通过，请重新连接或释放实例（日志：(.+)）$/s,
    format: (m, loc) => {
      const tail = m[1]!;
      switch (loc) {
        case "en-US":
          return `New service did not become ready and rollback health check failed. Reconnect or release the instance (log: ${tail})`;
        case "zh-TW":
          return `新服務尚未就緒且回復後健康檢查未通過，請重新連線或釋放執行個體（日誌：${tail}）`;
        case "zh-HK":
          return `新服務未就緒且回滾後健康檢查未通過，請重新連接或釋放實例（日誌：${tail}）`;
        case "ja-JP":
          return `新しいサービスが準備完了せず、ロールバック後のヘルスチェックにも失敗しました。再接続またはインスタンスを解放してください（ログ: ${tail}）`;
        case "ko-KR":
          return `새 서비스가 준비되지 않았고 롤백 후 상태 확인에도 실패했습니다. 다시 연결하거나 인스턴스를 해제하세요 (로그: ${tail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法字段键:\s*(.+)$/,
    format: (m, loc) => {
      const key = m[1];
      switch (loc) {
        case "en-US":
          return `Invalid field key: ${key}`;
        case "zh-TW":
          return `無效的欄位鍵：${key}`;
        case "zh-HK":
          return `無效的欄位鍵：${key}`;
        case "ja-JP":
          return `無効なフィールドキー: ${key}`;
        case "ko-KR":
          return `유효하지 않은 필드 키: ${key}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流节点数量不能超过 (\d+) 个$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `Workflow cannot contain more than ${n} nodes`;
        case "zh-TW":
          return `工作流程節點數量不可超過 ${n} 個`;
        case "zh-HK":
          return `工作流節點數量不能超過 ${n} 個`;
        case "ja-JP":
          return `ワークフローのノード数は ${n} 個を超えられません`;
        case "ko-KR":
          return `워크플로 노드 수는 ${n}개를 초과할 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流体积超过限制（最大 (\d+) MiB）$/,
    format: (m, loc) => {
      const n = m[1];
      switch (loc) {
        case "en-US":
          return `Workflow exceeds size limit (max ${n} MiB)`;
        case "zh-TW":
          return `工作流程大小超過限制（最大 ${n} MiB）`;
        case "zh-HK":
          return `工作流體積超過限制（最大 ${n} MiB）`;
        case "ja-JP":
          return `ワークフローがサイズ上限を超えています（最大 ${n} MiB）`;
        case "ko-KR":
          return `워크플로 크기가 제한을 초과했습니다 (최대 ${n} MiB)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法节点 ID:\s*(.+)$/,
    format: (m, loc) => {
      const id = m[1];
      switch (loc) {
        case "en-US":
          return `Invalid node ID: ${id}`;
        case "zh-TW":
          return `無效的節點 ID：${id}`;
        case "zh-HK":
          return `無效的節點 ID：${id}`;
        case "ja-JP":
          return `無効なノード ID: ${id}`;
        case "ko-KR":
          return `유효하지 않은 노드 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^节点 (\S+) 必须为对象$/,
    format: (m, loc) => {
      const id = m[1];
      switch (loc) {
        case "en-US":
          return `Node ${id} must be an object`;
        case "zh-TW":
          return `節點 ${id} 必須為物件`;
        case "zh-HK":
          return `節點 ${id} 必須為物件`;
        case "ja-JP":
          return `ノード ${id} はオブジェクトである必要があります`;
        case "ko-KR":
          return `노드 ${id}은(는) 객체여야 합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^节点 (\S+) 缺少有效的 class_type$/,
    format: (m, loc) => {
      const id = m[1];
      switch (loc) {
        case "en-US":
          return `Node ${id} is missing a valid class_type`;
        case "zh-TW":
          return `節點 ${id} 缺少有效的 class_type`;
        case "zh-HK":
          return `節點 ${id} 缺少有效的 class_type`;
        case "ja-JP":
          return `ノード ${id} に有効な class_type がありません`;
        case "ko-KR":
          return `노드 ${id}에 유효한 class_type이 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^节点 (\S+) 缺少有效的 inputs 对象$/,
    format: (m, loc) => {
      const id = m[1];
      switch (loc) {
        case "en-US":
          return `Node ${id} is missing a valid inputs object`;
        case "zh-TW":
          return `節點 ${id} 缺少有效的 inputs 物件`;
        case "zh-HK":
          return `節點 ${id} 缺少有效的 inputs 物件`;
        case "ja-JP":
          return `ノード ${id} に有効な inputs オブジェクトがありません`;
        case "ko-KR":
          return `노드 ${id}에 유효한 inputs 객체가 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^节点 (\S+) 包含非法输入名称$/,
    format: (m, loc) => {
      const id = m[1];
      switch (loc) {
        case "en-US":
          return `Node ${id} contains an invalid input name`;
        case "zh-TW":
          return `節點 ${id} 包含無效的輸入名稱`;
        case "zh-HK":
          return `節點 ${id} 包含無效的輸入名稱`;
        case "ja-JP":
          return `ノード ${id} に無効な入力名が含まれています`;
        case "ko-KR":
          return `노드 ${id}에 유효하지 않은 입력 이름이 포함되어 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^字段 (.+?) 包含非法浮点值$/,
    format: (m, loc) => {
      const key = m[1];
      switch (loc) {
        case "en-US":
          return `Field ${key} contains an invalid floating-point value`;
        case "zh-TW":
          return `欄位 ${key} 包含無效的浮點數值`;
        case "zh-HK":
          return `欄位 ${key} 包含無效的浮點數值`;
        case "ja-JP":
          return `フィールド ${key} に無効な浮動小数点値が含まれています`;
        case "ko-KR":
          return `필드 ${key}에 유효하지 않은 부동소수점 값이 포함되어 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法的 pip 依赖声明:\s*(.+)$/,
    format: (m, loc) => {
      const item = m[1];
      switch (loc) {
        case "en-US":
          return `Invalid pip dependency specifier: ${item}`;
        case "zh-TW":
          return `無效的 pip 相依套件宣告：${item}`;
        case "zh-HK":
          return `無效的 pip 依賴聲明：${item}`;
        case "ja-JP":
          return `無効な pip 依存指定: ${item}`;
        case "ko-KR":
          return `유효하지 않은 pip 종속성 선언: ${item}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^请在浏览器中使用 (.+) 勾选全部云盘权限并点击允许（仅本实例确认一次）$/,
    format: (m, loc) => {
      const email = m[1];
      switch (loc) {
        case "en-US":
          return `In your browser, sign in as ${email}, check all Drive permissions, and click Allow (once per instance)`;
        case "zh-TW":
          return `請在瀏覽器中使用 ${email} 勾選全部雲端硬碟權限並點擊允許（僅本執行個體確認一次）`;
        case "zh-HK":
          return `請在瀏覽器中使用 ${email} 勾選全部雲端硬碟權限並點擊允許（僅本實例確認一次）`;
        case "ja-JP":
          return `ブラウザで ${email} を使用し、ドライブ権限をすべて選択して「許可」をクリックしてください（インスタンスごとに 1 回）`;
        case "ko-KR":
          return `브라우저에서 ${email} 계정으로 모든 드라이브 권한을 선택하고 허용을 클릭하세요(인스턴스당 1회)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^暂未检测到 (.+) 的云盘授权确认：请确认浏览器中选择了同一 Google 账号，并在授权页勾选全部云盘权限后重试$/,
    format: (m, loc) => {
      const email = m[1];
      switch (loc) {
        case "en-US":
          return `Drive authorization for ${email} was not detected yet: make sure you chose the same Google account in your browser, checked all Drive permission boxes, and try again.`;
        case "zh-TW":
          return `暫未偵測到 ${email} 的雲端硬碟授權確認：請確認瀏覽器中選擇了同一 Google 帳戶，並在授權頁勾選全部雲端硬碟權限後重試`;
        case "zh-HK":
          return `暫未檢測到 ${email} 的雲端硬碟授權確認：請確認瀏覽器中選擇了同一 Google 帳戶，並在授權頁勾選全部雲端硬碟權限後重試`;
        case "ja-JP":
          return `${email} のドライブ認可がまだ確認できません。ブラウザで同じ Google アカウントを選び、すべてのドライブ権限にチェックを入れて再試行してください`;
        case "ko-KR":
          return `${email} 계정의 드라이브 승인이 아직 확인되지 않았습니다. 브라우저에서 동일한 Google 계정을 선택하고 모든 드라이브 권한을 체크한 뒤 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^等待浏览器确认 (.+) 的 Google 云盘授权超时。请确认浏览器中登录了同一 Google 账号并在授权页勾选全部云盘权限后重试；本机登录仍保留$/,
    format: (m, loc) => {
      const email = m[1];
      switch (loc) {
        case "en-US":
          return `Timed out waiting for Google Drive consent for ${email}. Make sure you signed into the same Google account in your browser and checked all Drive permissions, then retry; your local sign-in is preserved.`;
        case "zh-TW":
          return `等待瀏覽器確認 ${email} 的 Google 雲端硬碟授權逾時。請確認瀏覽器中登入了同一 Google 帳戶並在授權頁勾選全部雲端硬碟權限後重試；本機登入仍保留`;
        case "zh-HK":
          return `等待瀏覽器確認 ${email} 的 Google 雲端硬碟授權逾時。請確認瀏覽器中登入了同一 Google 帳戶並在授權頁勾選全部雲端硬碟權限後重試；本機登入仍保留`;
        case "ja-JP":
          return `${email} の Google ドライブ認可確認がタイムアウトしました。ブラウザで同じ Google アカウントにログインし、すべてのドライブ権限にチェックを入れて再試行してください（ローカルログインは保持されます）`;
        case "ko-KR":
          return `${email} 계정의 Google 드라이브 승인 대기 시간이 초과되었습니다. 브라우저에서 동일한 Google 계정으로 로그인하고 모든 드라이브 권한을 체크한 뒤 다시 시도하세요(로컬 로그인은 유지됩니다)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法素材 ID:\s*(.+)$/,
    format: (m, loc) => {
      const id = m[1];
      switch (loc) {
        case "en-US":
          return `Invalid asset ID: ${id}`;
        case "zh-TW":
          return `無效的素材 ID：${id}`;
        case "zh-HK":
          return `無效的素材 ID：${id}`;
        case "ja-JP":
          return `無効な素材 ID: ${id}`;
        case "ko-KR":
          return `유효하지 않은 자산 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^本次 GPU 分配请求将经由代理（(.+)），后续连接与推理均不走代理$/,
    format: (m, loc) => {
      const proxy = m[1];
      switch (loc) {
        case "en-US":
          return `Routing this GPU allocation request via proxy (${proxy}); subsequent kernel and inference traffic will bypass the proxy.`;
        case "zh-TW":
          return `本次 GPU 分配請求將經由代理（${proxy}），後續連線與推論皆不經過代理`;
        case "zh-HK":
          return `本次 GPU 分配請求將經由代理（${proxy}），後續連接及推理均不經代理`;
        case "ja-JP":
          return `今回の GPU 割り当て要求はプロキシ（${proxy}）を経由します（以降の接続と推論はプロキシを使用しません）`;
        case "ko-KR":
          return `이번 GPU 할당 요청은 프록시(${proxy})를 경유하며, 이후 연결 및 추론에는 프록시를 사용하지 않습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^分配代理连通性测试通过（(.+) · (\d+) ms）$/,
    format: (m, loc) => {
      const proxy = m[1];
      const ms = m[2];
      switch (loc) {
        case "en-US":
          return `Allocation proxy connectivity check passed (${proxy} · ${ms} ms)`;
        case "zh-TW":
          return `分配代理連線測試通過（${proxy} · ${ms} ms）`;
        case "zh-HK":
          return `分配代理連通性測試通過（${proxy} · ${ms} ms）`;
        case "ja-JP":
          return `割り当てプロキシの接続テストに成功しました（${proxy} · ${ms} ms）`;
        case "ko-KR":
          return `할당 프록시 연결 테스트 성공 (${proxy} · ${ms} ms)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^字段 (.+?) 的覆盖值类型不合法$/,
    format: (m, loc) => {
      const key = m[1];
      switch (loc) {
        case "en-US":
          return `Invalid override value type for field ${key}`;
        case "zh-TW":
          return `欄位 ${key} 的覆寫值類型無效`;
        case "zh-HK":
          return `欄位 ${key} 的覆蓋值類型無效`;
        case "ja-JP":
          return `フィールド ${key} の上書き値の型が無効です`;
        case "ko-KR":
          return `필드 ${key}의 재정의 값 유형이 올바르지 않습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^字段 (.+?) 不能为空值$/,
    format: (m, loc) => {
      const key = m[1];
      switch (loc) {
        case "en-US":
          return `Field ${key} cannot be null`;
        case "zh-TW":
          return `欄位 ${key} 不可為空值`;
        case "zh-HK":
          return `欄位 ${key} 不能為空值`;
        case "ja-JP":
          return `フィールド ${key} に null は指定できません`;
        case "ko-KR":
          return `필드 ${key}은(는) null일 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^未知字段[:：]\s*(.+)$/,
    format: (m, loc) => {
      const key = m[1];
      switch (loc) {
        case "en-US":
          return `Unknown field: ${key}`;
        case "zh-TW":
          return `未知欄位：${key}`;
        case "zh-HK":
          return `未知欄位：${key}`;
        case "ja-JP":
          return `不明なフィールド: ${key}`;
        case "ko-KR":
          return `알 수 없는 필드: ${key}`;
        default:
          return m[0];
      }
    },
  },

  // === console/internal/models/models.go ===
  {
    re: /^模型文件配置包含不支持的字段[:：]\s*(.+?)$/,
    format: (m, loc) => {
      const key = m[1];
      switch (loc) {
        case "en-US":
          return `Model file specification contains unsupported field: ${key}`;
        case "zh-TW":
          return `模型檔案設定包含不支援的欄位：${key}`;
        case "zh-HK":
          return `模型檔案設定包含不支援的欄位：${key}`;
        case "ja-JP":
          return `モデルファイル設定にサポートされていないフィールドが含まれています: ${key}`;
        case "ko-KR":
          return `모델 파일 설정에 지원하지 않는 필드가 포함되어 있습니다: ${key}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型文件字段 (.+?) 不能为空值$/,
    format: (m, loc) => {
      const key = m[1];
      switch (loc) {
        case "en-US":
          return `Model file field ${key} cannot be null`;
        case "zh-TW":
          return `模型檔案欄位 ${key} 不可為空值`;
        case "zh-HK":
          return `模型檔案欄位 ${key} 不能為空值`;
        case "ja-JP":
          return `モデルファイルフィールド ${key} に null は指定できません`;
        case "ko-KR":
          return `모델 파일 필드 ${key}은(는) null일 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型配置包含不支持的字段[:：]\s*(.+?)$/,
    format: (m, loc) => {
      const key = m[1];
      switch (loc) {
        case "en-US":
          return `Model specification contains unsupported field: ${key}`;
        case "zh-TW":
          return `模型設定包含不支援的欄位：${key}`;
        case "zh-HK":
          return `模型設定包含不支援的欄位：${key}`;
        case "ja-JP":
          return `モデル設定にサポートされていないフィールドが含まれています: ${key}`;
        case "ko-KR":
          return `모델 설정에 지원하지 않는 필드가 포함되어 있습니다: ${key}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型配置字段 (.+?) 不能为空值$/,
    format: (m, loc) => {
      const key = m[1];
      switch (loc) {
        case "en-US":
          return `Model specification field ${key} cannot be null`;
        case "zh-TW":
          return `模型設定欄位 ${key} 不可為空值`;
        case "zh-HK":
          return `模型設定欄位 ${key} 不能為空值`;
        case "ja-JP":
          return `モデル設定フィールド ${key} に null は指定できません`;
        case "ko-KR":
          return `모델 설정 필드 ${key}은(는) null일 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在按更新前规格自动重新装载模型 (.+?)(?:（((?:\d+K|\d+\s+tokens|上下文\s+\d+|温度\s+[^\s）·]+)(?:\s*·\s*(?:\d+K|\d+\s+tokens|上下文\s+\d+|温度\s+[^\s）·]+))*)）)?$/,
    format: (m, loc) => {
      const name = m[1];
      const rawSpec = m[2] || "";
      const formatSpec = (spec: string): string => {
        if (!spec) return "";
        const parts = spec.split(/\s*·\s*/).map((part) => {
          const ctxMatch = part.match(/^上下文\s+(\d+)$/);
          if (ctxMatch) {
            const n = ctxMatch[1];
            switch (loc) {
              case "en-US":
                return `context ${n}`;
              case "zh-TW":
              case "zh-HK":
                return `上下文 ${n}`;
              case "ja-JP":
                return `コンテキスト ${n}`;
              case "ko-KR":
                return `컨텍스트 ${n}`;
              default:
                return part;
            }
          }
          const tempMatch = part.match(/^温度\s+(.+)$/);
          if (tempMatch) {
            const t = tempMatch[1];
            switch (loc) {
              case "en-US":
                return `temperature ${t}`;
              case "zh-TW":
              case "zh-HK":
                return `溫度 ${t}`;
              case "ja-JP":
                return `温度 ${t}`;
              case "ko-KR":
                return `온도 ${t}`;
              default:
                return part;
            }
          }
          return part;
        });
        return parts.join(" · ");
      };
      const specText = formatSpec(rawSpec);
      switch (loc) {
        case "en-US":
          return specText
            ? `Automatically reloading model ${name} with previous settings (${specText})`
            : `Automatically reloading model ${name} with previous settings`;
        case "zh-TW":
          return specText
            ? `正在依更新前規格自動重新載入模型 ${name}（${specText}）`
            : `正在依更新前規格自動重新載入模型 ${name}`;
        case "zh-HK":
          return specText
            ? `正在按更新前規格自動重新載入模型 ${name}（${specText}）`
            : `正在按更新前規格自動重新載入模型 ${name}`;
        case "ja-JP":
          return specText
            ? `更新前の設定でモデル ${name} を自動再読み込みしています（${specText}）`
            : `更新前の設定でモデル ${name} を自動再読み込みしています`;
        case "ko-KR":
          return specText
            ? `업데이트 전 설정으로 모델 ${name}을(를) 자동으로 다시 불러오는 중입니다 (${specText})`
            : `업데이트 전 설정으로 모델 ${name}을(를) 자동으로 다시 불러오는 중입니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^自动重新装载模型 (.+?) 失败，请手动装载：(.+)$/s,
    format: (m, loc) => {
      const name = m[1];
      const detail = translateInner(m[2], loc);
      switch (loc) {
        case "en-US":
          return `Failed to reload model ${name} automatically; please load it manually: ${detail}`;
        case "zh-TW":
          return `自動重新載入模型 ${name} 失敗，請手動載入：${detail}`;
        case "zh-HK":
          return `自動重新載入模型 ${name} 失敗，請手動載入：${detail}`;
        case "ja-JP":
          return `モデル ${name} の自動再読み込みに失敗しました。手動で読み込んでください: ${detail}`;
        case "ko-KR":
          return `모델 ${name} 자동 다시 불러오기에 실패했습니다. 수동으로 불러와 주세요: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^自动恢复对外 API 失败，请手动开启：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to restore Public API automatically; please enable it manually: ${detail}`;
        case "zh-TW":
          return `自動還原對外 API 失敗，請手動開啟：${detail}`;
        case "zh-HK":
          return `自動恢復對外 API 失敗，請手動開啟：${detail}`;
        case "ja-JP":
          return `パブリック API の自動復元に失敗しました。手動で起動してください: ${detail}`;
        case "ko-KR":
          return `외부 API 자동 복원에 실패했습니다. 수동으로 켜 주세요: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^自动重新启动 ComfyUI 失败，请手动启动：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to restart ComfyUI automatically; please start it manually: ${detail}`;
        case "zh-TW":
          return `自動重新啟動 ComfyUI 失敗，請手動啟動：${detail}`;
        case "zh-HK":
          return `自動重新啟動 ComfyUI 失敗，請手動啟動：${detail}`;
        case "ja-JP":
          return `ComfyUI の自動再起動に失敗しました。手動で起動してください: ${detail}`;
        case "ko-KR":
          return `ComfyUI 자동 재시작에 실패했습니다. 수동으로 시작해 주세요: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^自动恢复 ComfyUI 原生画布失败，请手动开启：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to restore ComfyUI Native Canvas automatically; please enable it manually: ${detail}`;
        case "zh-TW":
          return `自動還原 ComfyUI 原生畫布失敗，請手動開啟：${detail}`;
        case "zh-HK":
          return `自動恢復 ComfyUI 原生畫布失敗，請手動開啟：${detail}`;
        case "ja-JP":
          return `ComfyUI ネイティブキャンバスの自動復元に失敗しました。手動で起動してください: ${detail}`;
        case "ko-KR":
          return `ComfyUI 네이티브 캔버스 자동 복원에 실패했습니다. 수동으로 켜 주세요: ${detail}`;
        default:
          return m[0];
      }
    },
  },
];

function applyRedactionTokens(text: string, locale: ColabOneLocale): string {
  if (locale === "zh-CN" || !text.includes("[")) return text;
  let out = text;
  for (const [token, table] of Object.entries(REDACTION_TOKENS)) {
    if (out.includes(token)) {
      out = out.split(token).join(table[locale]);
    }
  }
  return out;
}

function stripExceptionPrefix(text: string): string {
  return text
    .trim()
    .replace(
      /^(?:(?:Error|CallError|RuntimeError|TypeError|ValueError|LookupError|KeyError):\s*)+/i,
      "",
    )
    .trim();
}

function translateInner(trimmed: string, locale: ColabOneLocale): string {
  const cleaned = stripExceptionPrefix(trimmed);
  if (!cleaned) return trimmed;
  const exact = EXACT_MESSAGES[cleaned];
  if (exact) return exact[locale];
  if (locale === "zh-CN") return cleaned;
  for (const rule of PATTERN_RULES) {
    if (rule.when && !rule.when(cleaned)) continue;
    const m = cleaned.match(rule.re);
    if (m) return rule.format(m, locale);
  }
  return cleaned;
}

export function translateColabOneActivitySource(
  source: string | null | undefined,
  locale: ColabOneLocale,
): string {
  const key = (source || "").trim();
  if (!key) return "";
  const entry = SOURCE_LABELS[key];
  return entry ? entry[locale] : key;
}

export function translateColabOneBackendText(
  text: string | null | undefined,
  locale: ColabOneLocale,
): string {
  const raw = String(text ?? "");
  if (!raw) return raw;
  const trimmed = stripExceptionPrefix(raw);
  if (!trimmed) return raw;
  const translated = translateInner(trimmed, locale);
  return applyRedactionTokens(translated, locale);
}
