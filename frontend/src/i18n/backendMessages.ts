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
  音色试听: {
    "zh-CN": "音色试听",
    "en-US": "Voice preview",
    "zh-TW": "音色試聽",
    "zh-HK": "聲線試聽",
    "ja-JP": "ボイス試聴",
    "ko-KR": "음색 미리듣기",
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
  "Live Studio 根目录": {
    "zh-CN": "Live Studio 根目录",
    "en-US": "Live Studio root directory",
    "zh-TW": "Live Studio 根目錄",
    "zh-HK": "Live Studio 根目錄",
    "ja-JP": "Live Studio ルートディレクトリ",
    "ko-KR": "Live Studio 루트 디렉터리",
  },
  主播动作包目录: {
    "zh-CN": "主播动作包目录",
    "en-US": "Avatar action pack directory",
    "zh-TW": "主播動作套件目錄",
    "zh-HK": "主播動作套件目錄",
    "ja-JP": "アバターアクションパックディレクトリ",
    "ko-KR": "아바타 동작 팩 디렉터리",
  },
  驱动动作目录: {
    "zh-CN": "驱动动作目录",
    "en-US": "Driving motion directory",
    "zh-TW": "驅動動作目錄",
    "zh-HK": "驅動動作目錄",
    "ja-JP": "駆動モーションディレクトリ",
    "ko-KR": "구동 모션 디렉터리",
  },
  主播包目录: {
    "zh-CN": "主播包目录",
    "en-US": "Avatar pack directory",
    "zh-TW": "主播套件目錄",
    "zh-HK": "主播套件目錄",
    "ja-JP": "アバターパックディレクトリ",
    "ko-KR": "아바타 팩 디렉터리",
  },
  主播清单文件: {
    "zh-CN": "主播清单文件",
    "en-US": "Avatar manifest file",
    "zh-TW": "主播清單檔案",
    "zh-HK": "主播清單檔案",
    "ja-JP": "アバターマニフェストファイル",
    "ko-KR": "아바타 매니페스트 파일",
  },
  主播素材校验文件: {
    "zh-CN": "主播素材校验文件",
    "en-US": "Avatar asset checksum file",
    "zh-TW": "主播素材驗證檔案",
    "zh-HK": "主播素材驗證檔案",
    "ja-JP": "アバター素材検証ファイル",
    "ko-KR": "아바타 에셋 검증 파일",
  },
  人像输入文件: {
    "zh-CN": "人像输入文件",
    "en-US": "Portrait input file",
    "zh-TW": "人像輸入檔案",
    "zh-HK": "人像輸入檔案",
    "ja-JP": "ポートレート入力ファイル",
    "ko-KR": "인물 입력 파일",
  },
  主播人像文件: {
    "zh-CN": "主播人像文件",
    "en-US": "Avatar portrait file",
    "zh-TW": "主播人像檔案",
    "zh-HK": "主播人像檔案",
    "ja-JP": "アバターポートレートファイル",
    "ko-KR": "아바타 인물 파일",
  },
  动作片段目录: {
    "zh-CN": "动作片段目录",
    "en-US": "Action clips directory",
    "zh-TW": "動作片段目錄",
    "zh-HK": "動作片段目錄",
    "ja-JP": "アクションクリップディレクトリ",
    "ko-KR": "동작 클립 디렉터리",
  },
  背景音乐目录: {
    "zh-CN": "背景音乐目录",
    "en-US": "Background music directory",
    "zh-TW": "背景音樂目錄",
    "zh-HK": "背景音樂目錄",
    "ja-JP": "BGM ディレクトリ",
    "ko-KR": "배경 음악 디렉터리",
  },
  人像文件: {
    "zh-CN": "人像文件",
    "en-US": "Portrait file",
    "zh-TW": "人像檔案",
    "zh-HK": "人像檔案",
    "ja-JP": "ポートレートファイル",
    "ko-KR": "인물 파일",
  },
  主播包回收目录: {
    "zh-CN": "主播包回收目录",
    "en-US": "Avatar pack trash directory",
    "zh-TW": "主播套件回收目錄",
    "zh-HK": "主播套件回收目錄",
    "ja-JP": "アバターパックゴミ箱ディレクトリ",
    "ko-KR": "아바타 팩 휴지통 디렉터리",
  },
  主播包回收目标: {
    "zh-CN": "主播包回收目标",
    "en-US": "Avatar pack trash target",
    "zh-TW": "主播套件回收目標",
    "zh-HK": "主播套件回收目標",
    "ja-JP": "アバターパックゴミ箱移動先",
    "ko-KR": "아바타 팩 휴지통 대상",
  },
  动作视频文件: {
    "zh-CN": "动作视频文件",
    "en-US": "Action video file",
    "zh-TW": "動作影片檔案",
    "zh-HK": "動作影片檔案",
    "ja-JP": "アクション動画ファイル",
    "ko-KR": "동작 영상 파일",
  },
  背景音乐文件: {
    "zh-CN": "背景音乐文件",
    "en-US": "Background music file",
    "zh-TW": "背景音樂檔案",
    "zh-HK": "背景音樂檔案",
    "ja-JP": "BGM ファイル",
    "ko-KR": "배경 음악 파일",
  },
  驱动动作元数据: {
    "zh-CN": "驱动动作元数据",
    "en-US": "Driving motion metadata",
    "zh-TW": "驅動動作中繼資料",
    "zh-HK": "驅動動作元資料",
    "ja-JP": "駆動モーションメタデータ",
    "ko-KR": "구동 모션 메타데이터",
  },
  驱动视频文件: {
    "zh-CN": "驱动视频文件",
    "en-US": "Driving video file",
    "zh-TW": "驅動影片檔案",
    "zh-HK": "驅動影片檔案",
    "ja-JP": "駆動動画ファイル",
    "ko-KR": "구동 영상 파일",
  },
  驱动背景音乐文件: {
    "zh-CN": "驱动背景音乐文件",
    "en-US": "Driving background music file",
    "zh-TW": "驅動背景音樂檔案",
    "zh-HK": "驅動背景音樂檔案",
    "ja-JP": "駆動 BGM ファイル",
    "ko-KR": "구동 배경 음악 파일",
  },
  驱动动作回收目录: {
    "zh-CN": "驱动动作回收目录",
    "en-US": "Driving motion trash directory",
    "zh-TW": "驅動動作回收目錄",
    "zh-HK": "驅動動作回收目錄",
    "ja-JP": "駆動モーションゴミ箱ディレクトリ",
    "ko-KR": "구동 모션 휴지통 디렉터리",
  },
  驱动动作回收目标: {
    "zh-CN": "驱动动作回收目标",
    "en-US": "Driving motion trash target",
    "zh-TW": "驅動動作回收目標",
    "zh-HK": "驅動動作回收目標",
    "ja-JP": "駆動モーションゴミ箱移動先",
    "ko-KR": "구동 모션 휴지통 대상",
  },
  生成动作片段: {
    "zh-CN": "生成动作片段",
    "en-US": "Generated action clip",
    "zh-TW": "產生動作片段",
    "zh-HK": "生成動作片段",
    "ja-JP": "生成アクションクリップ",
    "ko-KR": "생성된 동작 클립",
  },
  目标目录: {
    "zh-CN": "目标目录",
    "en-US": "Target directory",
    "zh-TW": "目標目錄",
    "zh-HK": "目標目錄",
    "ja-JP": "対象ディレクトリ",
    "ko-KR": "대상 디렉터리",
  },
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
  "ComfyUI 节点包根目录": {
    "zh-CN": "ComfyUI 节点包根目录",
    "en-US": "ComfyUI node packs root directory",
    "zh-TW": "ComfyUI 節點套件根目錄",
    "zh-HK": "ComfyUI 節點套件根目錄",
    "ja-JP": "ComfyUI ノードパックルートディレクトリ",
    "ko-KR": "ComfyUI 노드 팩 루트 디렉터리",
  },
  "ComfyUI 安装目录": {
    "zh-CN": "ComfyUI 安装目录",
    "en-US": "ComfyUI installation directory",
    "zh-TW": "ComfyUI 安裝目錄",
    "zh-HK": "ComfyUI 安裝目錄",
    "ja-JP": "ComfyUI インストールディレクトリ",
    "ko-KR": "ComfyUI 설치 디렉터리",
  },
  "ComfyUI 自定义节点目录": {
    "zh-CN": "ComfyUI 自定义节点目录",
    "en-US": "ComfyUI custom nodes directory",
    "zh-TW": "ComfyUI 自訂節點目錄",
    "zh-HK": "ComfyUI 自訂節點目錄",
    "ja-JP": "ComfyUI カスタムノードディレクトリ",
    "ko-KR": "ComfyUI 사용자 지정 노드 디렉터리",
  },
  节点包目录: {
    "zh-CN": "节点包目录",
    "en-US": "Node pack directory",
    "zh-TW": "節點套件目錄",
    "zh-HK": "節點套件目錄",
    "ja-JP": "ノードパックディレクトリ",
    "ko-KR": "노드 팩 디렉터리",
  },
  托管节点清单文件: {
    "zh-CN": "托管节点清单文件",
    "en-US": "Managed nodes manifest file",
    "zh-TW": "託管節點清單檔案",
    "zh-HK": "託管節點清單檔案",
    "ja-JP": "管理対象ノードマニフェストファイル",
    "ko-KR": "관리 노드 매니페스트 파일",
  },
  "TTS 引擎目录": {
    "zh-CN": "TTS 引擎目录",
    "en-US": "TTS engines directory",
    "zh-TW": "TTS 引擎目錄",
    "zh-HK": "TTS 引擎目錄",
    "ja-JP": "TTS エンジンディレクトリ",
    "ko-KR": "TTS 엔진 디렉터리",
  },
  "TTS 引擎子目录": {
    "zh-CN": "TTS 引擎子目录",
    "en-US": "TTS engine subdirectory",
    "zh-TW": "TTS 引擎子目錄",
    "zh-HK": "TTS 引擎子目錄",
    "ja-JP": "TTS エンジンサブディレクトリ",
    "ko-KR": "TTS 엔진 하위 디렉터리",
  },
  "TTS 源码目录": {
    "zh-CN": "TTS 源码目录",
    "en-US": "TTS source directory",
    "zh-TW": "TTS 原始碼目錄",
    "zh-HK": "TTS 源碼目錄",
    "ja-JP": "TTS ソースディレクトリ",
    "ko-KR": "TTS 소스 디렉터리",
  },
  "TTS 工作目录": {
    "zh-CN": "TTS 工作目录",
    "en-US": "TTS working directory",
    "zh-TW": "TTS 工作目錄",
    "zh-HK": "TTS 工作目錄",
    "ja-JP": "TTS 作業ディレクトリ",
    "ko-KR": "TTS 작업 디렉터리",
  },
  "TTS 片段目录": {
    "zh-CN": "TTS 片段目录",
    "en-US": "TTS segments directory",
    "zh-TW": "TTS 片段目錄",
    "zh-HK": "TTS 片段目錄",
    "ja-JP": "TTS セグメントディレクトリ",
    "ko-KR": "TTS 구간 디렉터리",
  },
  "TTS 虚拟环境目录": {
    "zh-CN": "TTS 虚拟环境目录",
    "en-US": "TTS virtual environment directory",
    "zh-TW": "TTS 虛擬環境目錄",
    "zh-HK": "TTS 虛擬環境目錄",
    "ja-JP": "TTS 仮想環境ディレクトリ",
    "ko-KR": "TTS 가상 환경 디렉터리",
  },
  "TTS 引擎虚拟环境目录": {
    "zh-CN": "TTS 引擎虚拟环境目录",
    "en-US": "TTS engine virtual environment directory",
    "zh-TW": "TTS 引擎虛擬環境目錄",
    "zh-HK": "TTS 引擎虛擬環境目錄",
    "ja-JP": "TTS エンジン仮想環境ディレクトリ",
    "ko-KR": "TTS 엔진 가상 환경 디렉터리",
  },
  "TTS 源码依赖文件": {
    "zh-CN": "TTS 源码依赖文件",
    "en-US": "TTS source requirements file",
    "zh-TW": "TTS 原始碼相依套件檔案",
    "zh-HK": "TTS 源碼依賴檔案",
    "ja-JP": "TTS ソース依存関係ファイル",
    "ko-KR": "TTS 소스 종속성 파일",
  },
  "TTS 安装日志路径": {
    "zh-CN": "TTS 安装日志路径",
    "en-US": "TTS install log path",
    "zh-TW": "TTS 安裝日誌路徑",
    "zh-HK": "TTS 安裝日誌路徑",
    "ja-JP": "TTS インストールログパス",
    "ko-KR": "TTS 설치 로그 경로",
  },
  "TTS 依赖清单文件": {
    "zh-CN": "TTS 依赖清单文件",
    "en-US": "TTS dependencies manifest file",
    "zh-TW": "TTS 相依套件清單檔案",
    "zh-HK": "TTS 依賴清單檔案",
    "ja-JP": "TTS 依存関係マニフェストファイル",
    "ko-KR": "TTS 종속성 매니페스트 파일",
  },
  "TTS 工作脚本路径": {
    "zh-CN": "TTS 工作脚本路径",
    "en-US": "TTS worker script path",
    "zh-TW": "TTS 工作指令碼路徑",
    "zh-HK": "TTS 工作腳本路徑",
    "ja-JP": "TTS ワーカースクリプトパス",
    "ko-KR": "TTS 워커 스크립트 경로",
  },
  "TTS 配置文件路径": {
    "zh-CN": "TTS 配置文件路径",
    "en-US": "TTS config file path",
    "zh-TW": "TTS 設定檔路徑",
    "zh-HK": "TTS 配置檔路徑",
    "ja-JP": "TTS 設定ファイルパス",
    "ko-KR": "TTS 설정 파일 경로",
  },
  "TTS 日志文件路径": {
    "zh-CN": "TTS 日志文件路径",
    "en-US": "TTS log file path",
    "zh-TW": "TTS 日誌檔案路徑",
    "zh-HK": "TTS 日誌檔案路徑",
    "ja-JP": "TTS ログファイルパス",
    "ko-KR": "TTS 로그 파일 경로",
  },
  "TTS 就绪标记文件": {
    "zh-CN": "TTS 就绪标记文件",
    "en-US": "TTS readiness marker file",
    "zh-TW": "TTS 就緒標記檔案",
    "zh-HK": "TTS 就緒標記檔案",
    "ja-JP": "TTS 準備完了マーカーファイル",
    "ko-KR": "TTS 준비 완료 마커 파일",
  },
  音色参考音频: {
    "zh-CN": "音色参考音频",
    "en-US": "Voice reference audio",
    "zh-TW": "音色參考音訊",
    "zh-HK": "聲線參考音訊",
    "ja-JP": "ボイス参照音声",
    "ko-KR": "음색 참조 오디오",
  },
  情绪参考音频: {
    "zh-CN": "情绪参考音频",
    "en-US": "Emotion reference audio",
    "zh-TW": "情緒參考音訊",
    "zh-HK": "情緒參考音訊",
    "ja-JP": "感情参照音声",
    "ko-KR": "감정 참조 오디오",
  },
  参考音频转码目标: {
    "zh-CN": "参考音频转码目标",
    "en-US": "Reference audio transcode target",
    "zh-TW": "參考音訊轉碼目標",
    "zh-HK": "參考音訊轉碼目標",
    "ja-JP": "参照音声の変換先ファイル",
    "ko-KR": "참조 오디오 변환 대상 파일",
  },
  依赖约束目录: {
    "zh-CN": "依赖约束目录",
    "en-US": "Dependency constraints directory",
    "zh-TW": "相依套件約束目錄",
    "zh-HK": "依賴約束目錄",
    "ja-JP": "依存関係制約ディレクトリ",
    "ko-KR": "종속성 제약 디렉터리",
  },
  "PyTorch 约束文件": {
    "zh-CN": "PyTorch 约束文件",
    "en-US": "PyTorch constraints file",
    "zh-TW": "PyTorch 約束檔案",
    "zh-HK": "PyTorch 約束檔案",
    "ja-JP": "PyTorch 制約ファイル",
    "ko-KR": "PyTorch 제약 파일",
  },
  "ComfyUI 完成标记": {
    "zh-CN": "ComfyUI 完成标记",
    "en-US": "ComfyUI completion marker",
    "zh-TW": "ComfyUI 完成標記",
    "zh-HK": "ComfyUI 完成標記",
    "ja-JP": "ComfyUI 完了マーカー",
    "ko-KR": "ComfyUI 완료 마커",
  },
  模型路径配置文件: {
    "zh-CN": "模型路径配置文件",
    "en-US": "Model paths config file",
    "zh-TW": "模型路徑設定檔",
    "zh-HK": "模型路徑配置檔",
    "ja-JP": "モデルパス設定ファイル",
    "ko-KR": "모델 경로 설정 파일",
  },
  工作流文件: {
    "zh-CN": "工作流文件",
    "en-US": "Workflow file",
    "zh-TW": "工作流程檔案",
    "zh-HK": "工作流檔案",
    "ja-JP": "ワークフローファイル",
    "ko-KR": "워크플로 파일",
  },
  输入文件: {
    "zh-CN": "输入文件",
    "en-US": "Input file",
    "zh-TW": "輸入檔案",
    "zh-HK": "輸入檔案",
    "ja-JP": "入力ファイル",
    "ko-KR": "입력 파일",
  },
  作品文件: {
    "zh-CN": "作品文件",
    "en-US": "Output file",
    "zh-TW": "作品檔案",
    "zh-HK": "作品檔案",
    "ja-JP": "出力ファイル",
    "ko-KR": "결과물 파일",
  },
  作品: {
    "zh-CN": "作品",
    "en-US": "Output",
    "zh-TW": "作品",
    "zh-HK": "作品",
    "ja-JP": "出力",
    "ko-KR": "결과물",
  },
  输出缓存文件: {
    "zh-CN": "输出缓存文件",
    "en-US": "Output cache file",
    "zh-TW": "輸出快取檔案",
    "zh-HK": "輸出快取檔案",
    "ja-JP": "出力キャッシュファイル",
    "ko-KR": "출력 캐시 파일",
  },
  内置女声缓存目录: {
    "zh-CN": "内置女声缓存目录",
    "en-US": "Built-in female voice cache directory",
    "zh-TW": "內建女聲快取目錄",
    "zh-HK": "內置女聲快取目錄",
    "ja-JP": "組み込み女性ボイスキャッシュディレクトリ",
    "ko-KR": "기본 제공 여성 음색 캐시 디렉터리",
  },
  内置女声缓存文件: {
    "zh-CN": "内置女声缓存文件",
    "en-US": "Built-in female voice cache file",
    "zh-TW": "內建女聲快取檔案",
    "zh-HK": "內置女聲快取檔案",
    "ja-JP": "組み込み女性ボイスキャッシュファイル",
    "ko-KR": "기본 제공 여성 음색 캐시 파일",
  },
  内置女声生成临时路径: {
    "zh-CN": "内置女声生成临时路径",
    "en-US": "Built-in female voice temp path",
    "zh-TW": "內建女聲產生暫存路徑",
    "zh-HK": "內置女聲生成臨時路徑",
    "ja-JP": "組み込み女性ボイス生成の一時パス",
    "ko-KR": "기본 제공 여성 음색 임시 경로",
  },
  内置音色缓存目录: {
    "zh-CN": "内置音色缓存目录",
    "en-US": "Built-in voice cache directory",
    "zh-TW": "內建音色快取目錄",
    "zh-HK": "內置聲線快取目錄",
    "ja-JP": "組み込みボイスキャッシュディレクトリ",
    "ko-KR": "기본 제공 음색 캐시 디렉터리",
  },
  内置音色缓存文件: {
    "zh-CN": "内置音色缓存文件",
    "en-US": "Built-in voice cache file",
    "zh-TW": "內建音色快取檔案",
    "zh-HK": "內置聲線快取檔案",
    "ja-JP": "組み込みボイスキャッシュファイル",
    "ko-KR": "기본 제공 음색 캐시 파일",
  },
  内置音色生成临时路径: {
    "zh-CN": "内置音色生成临时路径",
    "en-US": "Built-in voice temp path",
    "zh-TW": "內建音色產生暫存路徑",
    "zh-HK": "內置聲線生成臨時路徑",
    "ja-JP": "組み込みボイス生成の一時パス",
    "ko-KR": "기본 제공 음색 임시 경로",
  },
  音色试听缓存目录: {
    "zh-CN": "音色试听缓存目录",
    "en-US": "Voice preview cache directory",
    "zh-TW": "音色試聽快取目錄",
    "zh-HK": "聲線試聽快取目錄",
    "ja-JP": "ボイス試聴キャッシュディレクトリ",
    "ko-KR": "음색 미리듣기 캐시 디렉터리",
  },
  音色试听缓存文件: {
    "zh-CN": "音色试听缓存文件",
    "en-US": "Voice preview cache file",
    "zh-TW": "音色試聽快取檔案",
    "zh-HK": "聲線試聽快取檔案",
    "ja-JP": "ボイス試聴キャッシュファイル",
    "ko-KR": "음색 미리듣기 캐시 파일",
  },
  试听音频缓存目录: {
    "zh-CN": "试听音频缓存目录",
    "en-US": "Preview audio cache directory",
    "zh-TW": "試聽音訊快取目錄",
    "zh-HK": "試聽音訊快取目錄",
    "ja-JP": "試聴音声キャッシュディレクトリ",
    "ko-KR": "미리듣기 오디오 캐시 디렉터리",
  },
  试听音频缓存文件: {
    "zh-CN": "试听音频缓存文件",
    "en-US": "Preview audio cache file",
    "zh-TW": "試聽音訊快取檔案",
    "zh-HK": "試聽音訊快取檔案",
    "ja-JP": "試聴音声キャッシュファイル",
    "ko-KR": "미리듣기 오디오 캐시 파일",
  },
  加速包缓存根目录: {
    "zh-CN": "加速包缓存根目录",
    "en-US": "Acceleration package cache root directory",
    "zh-TW": "加速套件快取根目錄",
    "zh-HK": "加速套件快取根目錄",
    "ja-JP": "高速化パッケージキャッシュルートディレクトリ",
    "ko-KR": "가속 패키지 캐시 루트 디렉터리",
  },
  加速包缓存目录: {
    "zh-CN": "加速包缓存目录",
    "en-US": "Acceleration package cache directory",
    "zh-TW": "加速套件快取目錄",
    "zh-HK": "加速套件快取目錄",
    "ja-JP": "高速化パッケージキャッシュディレクトリ",
    "ko-KR": "가속 패키지 캐시 디렉터리",
  },
  云盘加速包缓存根目录: {
    "zh-CN": "云盘加速包缓存根目录",
    "en-US": "Google Drive acceleration package cache root",
    "zh-TW": "雲端硬碟加速套件快取根目錄",
    "zh-HK": "雲端硬碟加速套件快取根目錄",
    "ja-JP": "Google ドライブ高速化パッケージキャッシュルート",
    "ko-KR": "Google 드라이브 가속 패키지 캐시 루트",
  },
  云盘加速包缓存目录: {
    "zh-CN": "云盘加速包缓存目录",
    "en-US": "Google Drive acceleration package cache directory",
    "zh-TW": "雲端硬碟加速套件快取目錄",
    "zh-HK": "雲端硬碟加速套件快取目錄",
    "ja-JP": "Google ドライブ高速化パッケージキャッシュディレクトリ",
    "ko-KR": "Google 드라이브 가속 패키지 캐시 디렉터리",
  },
  加速包构建目录: {
    "zh-CN": "加速包构建目录",
    "en-US": "Acceleration package build directory",
    "zh-TW": "加速套件建置目錄",
    "zh-HK": "加速套件構建目錄",
    "ja-JP": "高速化パッケージビルドディレクトリ",
    "ko-KR": "가속 패키지 빌드 디렉터리",
  },
  "SageAttention 源码目录": {
    "zh-CN": "SageAttention 源码目录",
    "en-US": "SageAttention source directory",
    "zh-TW": "SageAttention 原始碼目錄",
    "zh-HK": "SageAttention 源碼目錄",
    "ja-JP": "SageAttention ソースディレクトリ",
    "ko-KR": "SageAttention 소스 디렉터리",
  },
  "SageAttention .git 目录": {
    "zh-CN": "SageAttention .git 目录",
    "en-US": "SageAttention .git directory",
    "zh-TW": "SageAttention .git 目錄",
    "zh-HK": "SageAttention .git 目錄",
    "ja-JP": "SageAttention .git ディレクトリ",
    "ko-KR": "SageAttention .git 디렉터리",
  },
  "SageAttention 3 源码目录": {
    "zh-CN": "SageAttention 3 源码目录",
    "en-US": "SageAttention 3 source directory",
    "zh-TW": "SageAttention 3 原始碼目錄",
    "zh-HK": "SageAttention 3 源碼目錄",
    "ja-JP": "SageAttention 3 ソースディレクトリ",
    "ko-KR": "SageAttention 3 소스 디렉터리",
  },
  编译缓存目录: {
    "zh-CN": "编译缓存目录",
    "en-US": "Compile cache directory",
    "zh-TW": "編譯快取目錄",
    "zh-HK": "編譯快取目錄",
    "ja-JP": "コンパイルキャッシュディレクトリ",
    "ko-KR": "컴파일 캐시 디렉터리",
  },
  "Triton 编译缓存目录": {
    "zh-CN": "Triton 编译缓存目录",
    "en-US": "Triton compile cache directory",
    "zh-TW": "Triton 編譯快取目錄",
    "zh-HK": "Triton 編譯快取目錄",
    "ja-JP": "Triton コンパイルキャッシュディレクトリ",
    "ko-KR": "Triton 컴파일 캐시 디렉터리",
  },
  数字人引擎根目录: {
    "zh-CN": "数字人引擎根目录",
    "en-US": "Digital human engine root directory",
    "zh-TW": "數位人引擎根目錄",
    "zh-HK": "數碼人引擎根目錄",
    "ja-JP": "デジタルヒューマンエンジンルートディレクトリ",
    "ko-KR": "디지털 휴먼 엔진 루트 디렉터리",
  },
  数字人权重目录: {
    "zh-CN": "数字人权重目录",
    "en-US": "Digital human weights directory",
    "zh-TW": "數位人權重目錄",
    "zh-HK": "數碼人權重目錄",
    "ja-JP": "デジタルヒューマン重みディレクトリ",
    "ko-KR": "디지털 휴먼 가중치 디렉터리",
  },
  数字人源码目录: {
    "zh-CN": "数字人源码目录",
    "en-US": "Digital human source directory",
    "zh-TW": "數位人原始碼目錄",
    "zh-HK": "數碼人源碼目錄",
    "ja-JP": "デジタルヒューマンソースディレクトリ",
    "ko-KR": "디지털 휴먼 소스 디렉터리",
  },
  运行日志文件: {
    "zh-CN": "运行日志文件",
    "en-US": "Runtime log file",
    "zh-TW": "執行日誌檔案",
    "zh-HK": "運行日誌檔案",
    "ja-JP": "実行ログファイル",
    "ko-KR": "실행 로그 파일",
  },
  "Stage 2 工作目录": {
    "zh-CN": "Stage 2 工作目录",
    "en-US": "Stage 2 working directory",
    "zh-TW": "Stage 2 工作目錄",
    "zh-HK": "Stage 2 工作目錄",
    "ja-JP": "Stage 2 作業ディレクトリ",
    "ko-KR": "Stage 2 작업 디렉터리",
  },
  "Stage 2 输出清单": {
    "zh-CN": "Stage 2 输出清单",
    "en-US": "Stage 2 output manifest",
    "zh-TW": "Stage 2 輸出清單",
    "zh-HK": "Stage 2 輸出清單",
    "ja-JP": "Stage 2 出力マニフェスト",
    "ko-KR": "Stage 2 출력 매니페스트",
  },
  "Stage 2 配置文件": {
    "zh-CN": "Stage 2 配置文件",
    "en-US": "Stage 2 config file",
    "zh-TW": "Stage 2 設定檔",
    "zh-HK": "Stage 2 配置檔",
    "ja-JP": "Stage 2 設定ファイル",
    "ko-KR": "Stage 2 설정 파일",
  },
  "Stage 3 超分工作目录": {
    "zh-CN": "Stage 3 超分工作目录",
    "en-US": "Stage 3 upscaling working directory",
    "zh-TW": "Stage 3 超解析工作目錄",
    "zh-HK": "Stage 3 超分工作目錄",
    "ja-JP": "Stage 3 超解像作業ディレクトリ",
    "ko-KR": "Stage 3 업스케일 작업 디렉터리",
  },
  "Stage 3 超分输出清单": {
    "zh-CN": "Stage 3 超分输出清单",
    "en-US": "Stage 3 upscaling output manifest",
    "zh-TW": "Stage 3 超解析輸出清單",
    "zh-HK": "Stage 3 超分輸出清單",
    "ja-JP": "Stage 3 超解像出力マニフェスト",
    "ko-KR": "Stage 3 업스케일 출력 매니페스트",
  },
  "Stage 3 超分配置文件": {
    "zh-CN": "Stage 3 超分配置文件",
    "en-US": "Stage 3 upscaling config file",
    "zh-TW": "Stage 3 超解析設定檔",
    "zh-HK": "Stage 3 超分配置檔",
    "ja-JP": "Stage 3 超解像設定ファイル",
    "ko-KR": "Stage 3 업스케일 설정 파일",
  },
  "Stage 2 输出视频": {
    "zh-CN": "Stage 2 输出视频",
    "en-US": "Stage 2 output video",
    "zh-TW": "Stage 2 輸出影片",
    "zh-HK": "Stage 2 輸出影片",
    "ja-JP": "Stage 2 出力動画",
    "ko-KR": "Stage 2 출력 영상",
  },
  "Stage 3 超分输出视频": {
    "zh-CN": "Stage 3 超分输出视频",
    "en-US": "Stage 3 upscaled output video",
    "zh-TW": "Stage 3 超解析輸出影片",
    "zh-HK": "Stage 3 超分輸出影片",
    "ja-JP": "Stage 3 超解像出力動画",
    "ko-KR": "Stage 3 업스케일 출력 영상",
  },
  分段输出文件: {
    "zh-CN": "分段输出文件",
    "en-US": "Segment output file",
    "zh-TW": "分段輸出檔案",
    "zh-HK": "分段輸出檔案",
    "ja-JP": "セグメント出力ファイル",
    "ko-KR": "세그먼트 출력 파일",
  },
  分段视频缓存文件: {
    "zh-CN": "分段视频缓存文件",
    "en-US": "Segment video cache file",
    "zh-TW": "分段影片快取檔案",
    "zh-HK": "分段影片快取檔案",
    "ja-JP": "セグメント動画キャッシュファイル",
    "ko-KR": "세그먼트 영상 캐시 파일",
  },
  工作目录: {
    "zh-CN": "工作目录",
    "en-US": "Working directory",
    "zh-TW": "工作目錄",
    "zh-HK": "工作目錄",
    "ja-JP": "作業ディレクトリ",
    "ko-KR": "작업 디렉터리",
  },
  虚拟环境继承路径文件: {
    "zh-CN": "虚拟环境继承路径文件",
    "en-US": "Virtual environment inherited path file",
    "zh-TW": "虛擬環境繼承路徑檔案",
    "zh-HK": "虛擬環境繼承路徑檔案",
    "ja-JP": "仮想環境継承パスファイル",
    "ko-KR": "가상 환경 상속 경로 파일",
  },
  运行日志目录: {
    "zh-CN": "运行日志目录",
    "en-US": "Runtime log directory",
    "zh-TW": "執行日誌目錄",
    "zh-HK": "運行日誌目錄",
    "ja-JP": "実行ログディレクトリ",
    "ko-KR": "실행 로그 디렉터리",
  },
  数字人运行日志目录: {
    "zh-CN": "数字人运行日志目录",
    "en-US": "Digital human runtime log directory",
    "zh-TW": "數位人執行日誌目錄",
    "zh-HK": "數碼人運行日誌目錄",
    "ja-JP": "デジタルヒューマン実行ログディレクトリ",
    "ko-KR": "디지털 휴먼 실행 로그 디렉터리",
  },
  "Drive 缓存目录": {
    "zh-CN": "Drive 缓存目录",
    "en-US": "Drive cache directory",
    "zh-TW": "Drive 快取目錄",
    "zh-HK": "Drive 快取目錄",
    "ja-JP": "Drive キャッシュディレクトリ",
    "ko-KR": "Drive 캐시 디렉터리",
  },
  "Drive 权重目录": {
    "zh-CN": "Drive 权重目录",
    "en-US": "Drive weights directory",
    "zh-TW": "Drive 權重目錄",
    "zh-HK": "Drive 權重目錄",
    "ja-JP": "Drive モデル重みディレクトリ",
    "ko-KR": "Drive 가중치 디렉터리",
  },
  加速包缓存文件: {
    "zh-CN": "加速包缓存文件",
    "en-US": "Accelerator package cache file",
    "zh-TW": "加速套件快取檔案",
    "zh-HK": "加速包快取檔案",
    "ja-JP": "高速化パッケージキャッシュファイル",
    "ko-KR": "가속 패키지 캐시 파일",
  },
  加速包目标目录: {
    "zh-CN": "加速包目标目录",
    "en-US": "Accelerator package target directory",
    "zh-TW": "加速套件目標目錄",
    "zh-HK": "加速包目標目錄",
    "ja-JP": "高速化パッケージ対象ディレクトリ",
    "ko-KR": "가속 패키지 대상 디렉터리",
  },
  加速包目标文件: {
    "zh-CN": "加速包目标文件",
    "en-US": "Accelerator package target file",
    "zh-TW": "加速套件目標檔案",
    "zh-HK": "加速包目標檔案",
    "ja-JP": "高速化パッケージ対象ファイル",
    "ko-KR": "가속 패키지 대상 파일",
  },
  "加速包 wheel 文件": {
    "zh-CN": "加速包 wheel 文件",
    "en-US": "Accelerator package wheel file",
    "zh-TW": "加速套件 wheel 檔案",
    "zh-HK": "加速包 wheel 檔案",
    "ja-JP": "高速化パッケージ wheel ファイル",
    "ko-KR": "가속 패키지 wheel 파일",
  },
  下载临时文件: {
    "zh-CN": "下载临时文件",
    "en-US": "Download temporary file",
    "zh-TW": "下載暫存檔案",
    "zh-HK": "下載臨時檔案",
    "ja-JP": "ダウンロード一時ファイル",
    "ko-KR": "다운로드 임시 파일",
  },
  下载元数据文件: {
    "zh-CN": "下载元数据文件",
    "en-US": "Download metadata file",
    "zh-TW": "下載中繼資料檔案",
    "zh-HK": "下載元數據檔案",
    "ja-JP": "ダウンロードメタデータファイル",
    "ko-KR": "다운로드 메타데이터 파일",
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
  if (s.endsWith("路径") && s.length > 2) {
    const baseKey = s.slice(0, -2).trim();
    if (SYMLINK_DIR_LABELS[baseKey]) {
      const baseLabel = SYMLINK_DIR_LABELS[baseKey][loc];
      const zhSep = /[A-Za-z0-9]$/.test(baseLabel) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${baseLabel} path`;
        case "zh-TW":
          return `${baseLabel}${zhSep}路徑`;
        case "zh-HK":
          return `${baseLabel}${zhSep}路徑`;
        case "ja-JP":
          return `${baseLabel} パス`;
        case "ko-KR":
          return `${baseLabel} 경로`;
        default:
          return s;
      }
    }
  }
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
  const customNode = s.match(/^自定义节点目录 (.+)$/);
  if (customNode) {
    const dir = customNode[1];
    switch (loc) {
      case "en-US":
        return `Custom node directory ${dir}`;
      case "zh-TW":
        return `自訂節點目錄 ${dir}`;
      case "zh-HK":
        return `自訂節點目錄 ${dir}`;
      case "ja-JP":
        return `カスタムノードディレクトリ ${dir}`;
      case "ko-KR":
        return `사용자 지정 노드 디렉터리 ${dir}`;
      default:
        return s;
    }
  }
  const packDir = s.match(/^节点包目录 (.+)$/);
  if (packDir) {
    const dir = packDir[1];
    switch (loc) {
      case "en-US":
        return `Node pack directory ${dir}`;
      case "zh-TW":
        return `節點套件目錄 ${dir}`;
      case "zh-HK":
        return `節點套件目錄 ${dir}`;
      case "ja-JP":
        return `ノードパックディレクトリ ${dir}`;
      case "ko-KR":
        return `노드 팩 디렉터리 ${dir}`;
      default:
        return s;
    }
  }
  const ttsSub = s.match(/^TTS (引擎目录|引擎子目录|虚拟环境目录) (.+)$/);
  if (ttsSub) {
    const base = SYMLINK_DIR_LABELS[`TTS ${ttsSub[1]}`]?.[loc] ?? `TTS ${ttsSub[1]}`;
    return `${base} ${ttsSub[2]}`;
  }
  const weightSubdir = s.match(/^(本地权重目录|Drive 权重目录)（([^）]+)）$/);
  if (weightSubdir) {
    const base = weightSubdir[1]!;
    const sub = weightSubdir[2]!;
    if (base === "本地权重目录") {
      switch (loc) {
        case "en-US":
          return `Local weights directory (${sub})`;
        case "zh-TW":
          return `本機權重目錄（${sub}）`;
        case "zh-HK":
          return `本地權重目錄（${sub}）`;
        case "ja-JP":
          return `ローカルモデル重みディレクトリ（${sub}）`;
        case "ko-KR":
          return `로컬 가중치 디렉터리 (${sub})`;
        default:
          return s;
      }
    }
    switch (loc) {
      case "en-US":
        return `Drive weights directory (${sub})`;
      case "zh-TW":
        return `Drive 權重目錄（${sub}）`;
      case "zh-HK":
        return `Drive 權重目錄（${sub}）`;
      case "ja-JP":
        return `Drive モデル重みディレクトリ（${sub}）`;
      case "ko-KR":
        return `Drive 가중치 디렉터리 (${sub})`;
      default:
        return s;
    }
  }
  const dynamicSuffix = s.match(
    /^(.+?)\s*(源码目录|源码就绪标记|Git 元数据目录|虚拟环境目录|环境就绪标记|权重目录|权重标记|日志文件|输出清单|输出视频)$/,
  );
  if (dynamicSuffix) {
    const prefix = translateInner(dynamicSuffix[1]!.trim(), loc);
    const suffixKey = dynamicSuffix[2]!;
    const suffixTable: Record<string, ColabOneLocaleTable> = {
      源码目录: {
        "zh-CN": "源码目录",
        "en-US": "source directory",
        "zh-TW": "原始碼目錄",
        "zh-HK": "源碼目錄",
        "ja-JP": "ソースディレクトリ",
        "ko-KR": "소스 디렉터리",
      },
      源码就绪标记: {
        "zh-CN": "源码就绪标记",
        "en-US": "source ready marker",
        "zh-TW": "原始碼就緒標記",
        "zh-HK": "源碼就緒標記",
        "ja-JP": "ソース準備完了マーカー",
        "ko-KR": "소스 준비 완료 마커",
      },
      "Git 元数据目录": {
        "zh-CN": "Git 元数据目录",
        "en-US": "Git metadata directory",
        "zh-TW": "Git 中繼資料目錄",
        "zh-HK": "Git 元數據目錄",
        "ja-JP": "Git メタデータディレクトリ",
        "ko-KR": "Git 메타데이터 디렉터리",
      },
      虚拟环境目录: {
        "zh-CN": "虚拟环境目录",
        "en-US": "virtual environment directory",
        "zh-TW": "虛擬環境目錄",
        "zh-HK": "虛擬環境目錄",
        "ja-JP": "仮想環境ディレクトリ",
        "ko-KR": "가상 환경 디렉터리",
      },
      环境就绪标记: {
        "zh-CN": "环境就绪标记",
        "en-US": "environment ready marker",
        "zh-TW": "環境就緒標記",
        "zh-HK": "環境就緒標記",
        "ja-JP": "環境準備完了マーカー",
        "ko-KR": "환경 준비 완료 마커",
      },
      权重目录: {
        "zh-CN": "权重目录",
        "en-US": "weights directory",
        "zh-TW": "權重目錄",
        "zh-HK": "權重目錄",
        "ja-JP": "モデル重みディレクトリ",
        "ko-KR": "가중치 디렉터리",
      },
      权重标记: {
        "zh-CN": "权重标记",
        "en-US": "weights marker",
        "zh-TW": "權重標記",
        "zh-HK": "權重標記",
        "ja-JP": "モデル重みマーカー",
        "ko-KR": "가중치 마커",
      },
      日志文件: {
        "zh-CN": "日志文件",
        "en-US": "log file",
        "zh-TW": "日誌檔案",
        "zh-HK": "日誌檔案",
        "ja-JP": "ログファイル",
        "ko-KR": "로그 파일",
      },
      输出清单: {
        "zh-CN": "输出清单",
        "en-US": "output manifest",
        "zh-TW": "輸出清單",
        "zh-HK": "輸出清單",
        "ja-JP": "出力マニフェスト",
        "ko-KR": "출력 매니フェスト",
      },
      输出视频: {
        "zh-CN": "输出视频",
        "en-US": "output video",
        "zh-TW": "輸出影片",
        "zh-HK": "輸出影片",
        "ja-JP": "出力動画",
        "ko-KR": "출력 영상",
      },
    };
    const suffix = suffixTable[suffixKey]?.[loc] ?? suffixKey;
    if (loc === "zh-CN") return s;
    if (loc === "zh-TW" || loc === "zh-HK") {
      const zhSep = /[A-Za-z0-9]$/.test(prefix) ? " " : "";
      return `${prefix}${zhSep}${suffix}`;
    }
    return `${prefix} ${suffix}`;
  }
  return translateInner(s, loc);
}

function formatSegmentLabel(
  cur: string,
  total: string,
  loc: ColabOneLocale,
): string {
  switch (loc) {
    case "en-US":
      return `Segment ${cur}/${total}`;
    case "zh-TW":
    case "zh-HK":
      return `第 ${cur}/${total} 段`;
    case "ja-JP":
      return `セグメント ${cur}/${total}`;
    case "ko-KR":
      return `세그먼트 ${cur}/${total}`;
    default:
      return `第 ${cur}/${total} 段`;
  }
}

function translateCommitLabel(raw: string, loc: ColabOneLocale): string {
  const s = raw.trim();
  if (s === "提交哈希") {
    return {
      "zh-CN": s,
      "en-US": "Commit hash",
      "zh-TW": "提交雜湊",
      "zh-HK": "提交哈希",
      "ja-JP": "コミットハッシュ",
      "ko-KR": "커밋 해시",
    }[loc];
  }
  if (s === "模型修订版本") {
    return {
      "zh-CN": s,
      "en-US": "Model revision",
      "zh-TW": "模型修訂版本",
      "zh-HK": "模型修訂版本",
      "ja-JP": "モデルリビジョン",
      "ko-KR": "모델 리비전",
    }[loc];
  }
  const nodeCommit = s.match(/^节点 (.+?) 提交哈希$/);
  if (nodeCommit) {
    const name = nodeCommit[1];
    return {
      "zh-CN": s,
      "en-US": `Node ${name} commit hash`,
      "zh-TW": `節點 ${name} 提交雜湊`,
      "zh-HK": `節點 ${name} 提交哈希`,
      "ja-JP": `ノード ${name} のコミットハッシュ`,
      "ko-KR": `노드 ${name} 커밋 해시`,
    }[loc];
  }
  return s;
}

function translateGpuVramDesc(raw: string, loc: ColabOneLocale): string {
  const s = raw.trim();
  const paren = s.match(/^(.+?)（([^）]+)）$/);
  if (paren) {
    const gpu = translateInner(paren[1]!, loc);
    const mem = paren[2]!;
    if (loc === "en-US" || loc === "ko-KR") {
      return `${gpu} (${mem})`;
    }
    return `${gpu}（${mem}）`;
  }
  return translateInner(s, loc);
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

function translateLongCatAccelToken(raw: string, loc: ColabOneLocale): string {
  const s = raw.trim();
  if (loc === "zh-CN" || !s) return s;
  const known: Record<string, ColabOneLocaleTable> = {
    关闭: {
      "zh-CN": "关闭",
      "en-US": "disabled",
      "zh-TW": "停用",
      "zh-HK": "關閉",
      "ja-JP": "無効",
      "ko-KR": "비활성화",
    },
    禁用: {
      "zh-CN": "禁用",
      "en-US": "disabled",
      "zh-TW": "停用",
      "zh-HK": "停用",
      "ja-JP": "無効",
      "ko-KR": "비활성화",
    },
    无: {
      "zh-CN": "无",
      "en-US": "none",
      "zh-TW": "無",
      "zh-HK": "無",
      "ja-JP": "なし",
      "ko-KR": "없음",
    },
  };
  return known[s]?.[loc] ?? translateInner(s, loc);
}

function translateLongCatFallbackTarget(
  raw: string,
  loc: ColabOneLocale,
): string {
  const s = raw.trim();
  if (loc === "zh-CN" || !s) return s;
  if (EXACT_MESSAGES[s]) return EXACT_MESSAGES[s][loc];
  const known: Record<string, ColabOneLocaleTable> = {
    高速稳健: {
      "zh-CN": "高速稳健",
      "en-US": "Fast & Stable",
      "zh-TW": "高速穩健",
      "zh-HK": "高速穩健",
      "ja-JP": "高速・安定",
      "ko-KR": "고속 안정",
    },
    兼容保底: {
      "zh-CN": "兼容保底",
      "en-US": "Compatibility Fallback",
      "zh-TW": "相容保底",
      "zh-HK": "兼容保底",
      "ja-JP": "互換フォールバック",
      "ko-KR": "호환성 폴백",
    },
    兼容模式: {
      "zh-CN": "兼容模式",
      "en-US": "Compatibility Mode",
      "zh-TW": "相容模式",
      "zh-HK": "兼容模式",
      "ja-JP": "互換モード",
      "ko-KR": "호환 모드",
    },
    "原生 BF16 + SDPA": {
      "zh-CN": "原生 BF16 + SDPA",
      "en-US": "Native BF16 + SDPA",
      "zh-TW": "原生 BF16 + SDPA",
      "zh-HK": "原生 BF16 + SDPA",
      "ja-JP": "ネイティブ BF16 + SDPA",
      "ko-KR": "네이티브 BF16 + SDPA",
    },
    "关闭 torch.compile": {
      "zh-CN": "关闭 torch.compile",
      "en-US": "torch.compile off",
      "zh-TW": "關閉 torch.compile",
      "zh-HK": "關閉 torch.compile",
      "ja-JP": "torch.compile オフ",
      "ko-KR": "torch.compile 끔",
    },
    关闭编译: {
      "zh-CN": "关闭编译",
      "en-US": "compile off",
      "zh-TW": "關閉編譯",
      "zh-HK": "關閉編譯",
      "ja-JP": "コンパイルオフ",
      "ko-KR": "컴파일 끔",
    },
    跳过编译预热: {
      "zh-CN": "跳过编译预热",
      "en-US": "skip compile warmup",
      "zh-TW": "略過編譯預熱",
      "zh-HK": "跳過編譯預熱",
      "ja-JP": "コンパイル予熱をスキップ",
      "ko-KR": "컴파일 예열 생략",
    },
  };
  if (known[s]) return known[s][loc];
  const paren = s.match(/^(.+?)（([^）]+)）$/);
  if (paren) {
    const head = translateLongCatFallbackTarget(paren[1]!, loc);
    const note = translateLongCatFallbackTarget(paren[2]!, loc);
    return loc === "en-US" || loc === "ko-KR"
      ? `${head} (${note})`
      : `${head}（${note}）`;
  }
  const modeMatch = s.match(/^(.+?)\s*模式$/);
  if (modeMatch) {
    const base = translateLongCatFallbackTarget(modeMatch[1]!, loc);
    switch (loc) {
      case "en-US":
        return `${base} mode`;
      case "zh-TW":
      case "zh-HK":
        return `${base} 模式`;
      case "ja-JP":
        return `${base} モード`;
      case "ko-KR":
        return `${base} 모드`;
      default:
        return s;
    }
  }
  return translateInner(s, loc);
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
  const retryCapMatch = s.match(/^分配重试上限:\s*(\d+) 次$/);
  if (retryCapMatch) {
    const count = retryCapMatch[1];
    return {
      "zh-CN": s,
      "en-US": `Max allocation attempts: ${count}`,
      "zh-TW": `分配重試上限: ${count} 次`,
      "zh-HK": `分配重試上限: ${count} 次`,
      "ja-JP": `割り当て試行上限: ${count} 回`,
      "ko-KR": `할당 시도 상한: ${count}회`,
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
  const compMatch = s.match(/^自动压缩 (\d+) 至 (\d+) tokens$/);
  if (compMatch) {
    const [, before, after] = compMatch;
    return {
      "zh-CN": s,
      "en-US": `auto-compacted ${before} to ${after} tokens`,
      "zh-TW": `自動壓縮 ${before} 至 ${after} tokens`,
      "zh-HK": `自動壓縮 ${before} 至 ${after} tokens`,
      "ja-JP": `自動圧縮 ${before}〜${after} tokens`,
      "ko-KR": `자동 압축 ${before}~${after} tokens`,
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
    const val = translateZhDuration(durMatch[1]!, loc);
    return {
      "zh-CN": `耗时 ${val}`,
      "en-US": `elapsed ${val}`,
      "zh-TW": `耗時 ${val}`,
      "zh-HK": `耗時 ${val}`,
      "ja-JP": `所要 ${val}`,
      "ko-KR": `소요 ${val}`,
    }[loc];
  }
  return s;
}

function formatIntegerSecondsForLocale(
  totalSeconds: number,
  loc: ColabOneLocale,
): string {
  const safe = Math.max(0, Math.floor(totalSeconds));
  if (safe < 60) {
    switch (loc) {
      case "en-US":
        return `${safe}s`;
      case "ko-KR":
        return `${safe}초`;
      default:
        return `${safe} 秒`;
    }
  }
  const hours = Math.floor(safe / 3600);
  const mins = Math.floor((safe % 3600) / 60);
  const secs = safe % 60;
  if (hours === 0) {
    if (secs === 0) {
      switch (loc) {
        case "en-US":
          return `${mins} min`;
        case "zh-TW":
        case "zh-HK":
          return `${mins} 分鐘`;
        case "ja-JP":
          return `${mins} 分`;
        case "ko-KR":
          return `${mins}분`;
        default:
          return `${mins} 分钟`;
      }
    }
    switch (loc) {
      case "en-US":
        return `${mins}m ${secs}s`;
      case "ko-KR":
        return `${mins}분 ${secs}초`;
      default:
        return `${mins} 分 ${secs} 秒`;
    }
  }
  if (mins === 0 && secs === 0) {
    switch (loc) {
      case "en-US":
        return `${hours} hr`;
      case "zh-TW":
      case "zh-HK":
        return `${hours} 小時`;
      case "ja-JP":
        return `${hours} 時間`;
      case "ko-KR":
        return `${hours}시간`;
      default:
        return `${hours} 小时`;
    }
  }
  if (secs === 0) {
    switch (loc) {
      case "en-US":
        return `${hours}h ${mins}m`;
      case "zh-TW":
      case "zh-HK":
        return `${hours} 小時 ${mins} 分`;
      case "ja-JP":
        return `${hours} 時間 ${mins} 分`;
      case "ko-KR":
        return `${hours}시간 ${mins}분`;
      default:
        return `${hours} 小时 ${mins} 分`;
    }
  }
  switch (loc) {
    case "en-US":
      return `${hours}h ${mins}m ${secs}s`;
    case "zh-TW":
    case "zh-HK":
      return `${hours} 小時 ${mins} 分 ${secs} 秒`;
    case "ja-JP":
      return `${hours} 時間 ${mins} 分 ${secs} 秒`;
    case "ko-KR":
      return `${hours}시간 ${mins}분 ${secs}초`;
    default:
      return `${hours} 小时 ${mins} 分 ${secs} 秒`;
  }
}

function translateZhDuration(raw: string, loc: ColabOneLocale): string {
  const s = raw.trim();
  const hms = s.match(/^(\d+)\s*小时\s*(\d+)\s*分\s*(\d+)\s*秒$/);
  if (hms) {
    const [, h, m, sec] = hms;
    switch (loc) {
      case "en-US":
        return `${h}h ${m}m ${sec}s`;
      case "zh-TW":
      case "zh-HK":
        return `${h} 小時 ${m} 分 ${sec} 秒`;
      case "ja-JP":
        return `${h} 時間 ${m} 分 ${sec} 秒`;
      case "ko-KR":
        return `${h}시간 ${m}분 ${sec}초`;
      default:
        return `${h} 小时 ${m} 分 ${sec} 秒`;
    }
  }
  const hm = s.match(/^(\d+)\s*小时\s*(\d+)\s*分(?:钟)?$/);
  if (hm) {
    const [, h, m] = hm;
    switch (loc) {
      case "en-US":
        return `${h}h ${m}m`;
      case "zh-TW":
      case "zh-HK":
        return `${h} 小時 ${m} 分`;
      case "ja-JP":
        return `${h} 時間 ${m} 分`;
      case "ko-KR":
        return `${h}시간 ${m}분`;
      default:
        return `${h} 小时 ${m} 分`;
    }
  }
  const hOnly = s.match(/^(\d+)\s*小时$/);
  if (hOnly) {
    const h = hOnly[1]!;
    switch (loc) {
      case "en-US":
        return `${h} hr`;
      case "zh-TW":
      case "zh-HK":
        return `${h} 小時`;
      case "ja-JP":
        return `${h} 時間`;
      case "ko-KR":
        return `${h}시간`;
      default:
        return `${h} 小时`;
    }
  }
  const ms = s.match(/^(\d+)\s*分\s*(\d+)\s*秒$/);
  if (ms) {
    const [, m, sec] = ms;
    switch (loc) {
      case "en-US":
        return `${m}m ${sec}s`;
      case "ko-KR":
        return `${m}분 ${sec}초`;
      default:
        return `${m} 分 ${sec} 秒`;
    }
  }
  const mOnly = s.match(/^(\d+)\s*分钟$/);
  if (mOnly) {
    const m = mOnly[1]!;
    switch (loc) {
      case "en-US":
        return `${m} min`;
      case "zh-TW":
      case "zh-HK":
        return `${m} 分鐘`;
      case "ja-JP":
        return `${m} 分`;
      case "ko-KR":
        return `${m}분`;
      default:
        return `${m} 分钟`;
    }
  }
  const secOnly = s.match(/^([\d.]+)\s*秒$/);
  if (secOnly) {
    const val = secOnly[1]!;
    if (/^\d+$/.test(val)) {
      const n = Number(val);
      if (Number.isFinite(n) && n >= 60) {
        return formatIntegerSecondsForLocale(n, loc);
      }
    }
    switch (loc) {
      case "en-US":
        return `${val}s`;
      case "ko-KR":
        return `${val}초`;
      default:
        return `${val} 秒`;
    }
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

const HAN_CHAR_RE = /[\u3400-\u9fff\uf900-\ufaff]/g;

function countHanChars(text: string): number {
  return text.match(HAN_CHAR_RE)?.length ?? 0;
}

const COMFY_WORKFLOW_NAMES = new Set<string>([
  "口播数字人 · 长视频推荐版",
  "口播数字人 · 旗舰高清版",
  "口播数字人 · 96GB 电影级旗舰版",
  "口播数字人 · InfiniteTalk 长视频版（配 AI 2x 超分）",
  "口播数字人 · LongCat-Avatar 旗舰高清版（原生 720p）",
]);

const COMFY_WS_NODE_OR_PHASE_LABELS = new Set<string>([
  // server/comfyui.py WebSocket phase_labels
  "正在启动执行",
  "复用已缓存节点",
  "远端 GPU 执行中",
  "节点已完成",
  "采样中",
  // catalog/comfyui.json & server/digital_human.py workflow node titles
  "主播形象图",
  "缩放主播画面",
  "加载 Wan2.1 VAE",
  "画面与口播风格提示词",
  "加载 CLIP Vision",
  "编码主播视觉特征",
  "加载中文 Wav2Vec2",
  "提取音频口型特征",
  "口播驱动音频",
  "加载 InfiniteTalk 音频驱动权重",
  "合成有声 MP4 视频",
  "加载 LightX2V 蒸馏加速 LoRA",
  "加载 Wan2.1-I2V-14B 主模型",
  "InfiniteTalk 滑窗条件编码",
  "InfiniteTalk 扩散采样器",
  "VAE 视频解码",
  "编码首帧参考潜空间",
  "Whisper 音频编码器",
  "LongCat Whisper 音频特征",
  "LongCat 音视频融合条件",
  "加载 LongCat DMD 蒸馏 LoRA",
  "加载 LongCat-Avatar 1.5 主模型",
  "LongCat 蒸馏调度器",
  "LongCat-Avatar 扩散采样器",
  "AI 超分模型加载",
  "AI 2x 细节超分",
  "目标分辨率精调",
  "torch.compile 算子加速",
  "原始人像参考图（锁脸）",
  "原始人像参考潜变量",
  "正在准备远端原生 LongCat 1.6 环境与权重",
  "正在执行 Kim_Vocal_2 前置人声分离",
  "正在执行原生 720p LongCat 1.6 渲染",
  "正在准备远端全画幅超分环境与权重",
  "正在清理上一阶段显存以加载全画幅超分模型",
  "正在执行全画幅时空扩散超分",
  "正在执行终稿电影级封装与 48 kHz 高保真音画合流",
  "正在准备 LongCat-Video-Avatar 1.6 原生运行环境",
  "LongCat-Video-Avatar 1.6 原生环境与权重已就绪",
  "正在启动 LongCat-Video-Avatar 1.6 原生渲染进程",
  "正在启动远端扩散视频超分进程",
  "正在释放远端 ComfyUI 显存以独占 96GB 跑原生旗舰引擎",
  "正在加载纯 BF16 LongCat-Video-Avatar 1.6 模型权重",
  "正在加载 BF16 Avatar 1.6 DiT 主模型",
  "正在配置 torch.compile 无损算子编译",
  "正在提取全局 Whisper-Large-v3 音频特征",
  "正在封装无损 FFV1 中间视频并写入输出清单",
  "正在将 SeedVR2 超分帧封装为无损 FFV1 中间视频",
  "正在将 FlashVSR+ 超分帧封装为无损 FFV1 中间视频",
]);

const UNHANDLED_PROGRESS_TAIL_RE =
  /^(?:已用(?:时)?\s*\d+\s*(?:秒|s)|排队中|正在渲染|采样\s+\d+\/\d+|耗时\s*[\d.]+|进度\s+[\d.]+%)/;

function translateTaskTail(
  tail: string,
  loc: ColabOneLocale,
  status: string,
): string {
  const trimmed = tail.trim();
  if (!trimmed) return "";

  const whole = translateInner(trimmed, loc);
  const parts = trimmed.split(" · ");
  if (parts.length < 2) return whole;

  // Never split an exact-catalog entry (e.g. "口播数字人 · 长视频推荐版").
  if (Object.prototype.hasOwnProperty.call(EXACT_MESSAGES, trimmed)) {
    return whole;
  }

  // Active and completed tasks only carry task.message (no appended task.error).
  // Translate the entire tail first so built-in middot workflow names are never sliced.
  if (status === "进行中" || status === "已完成") {
    if (whole !== trimmed) return whole;
    for (let i = parts.length - 1; i >= 1; i--) {
      const left = parts.slice(0, i).join(" · ");
      const right = parts.slice(i).join(" · ");
      const leftT = translateInner(left, loc);
      const rightT = translateInner(right, loc);
      if (leftT !== left && rightT !== right) {
        return `${leftT} · ${rightT}`;
      }
    }
    for (let i = parts.length - 1; i >= 1; i--) {
      const left = parts.slice(0, i).join(" · ");
      const right = parts.slice(i).join(" · ");
      const leftT = translateInner(left, loc);
      const rightT = translateInner(right, loc);
      if (leftT !== left || rightT !== right) {
        return `${leftT} · ${rightT}`;
      }
    }
    return whole;
  }

  // Failed / cancelled tasks may combine "message · error" or carry only one of them.
  // Score `whole` vs each split candidate by untranslated Han count (in loc for
  // en-US/ko-KR, or en-US as a structural completeness oracle for CJK locales) so we
  // separate appended errors without slicing built-in middot workflow names in half.
  const scoreLocale: ColabOneLocale =
    loc === "en-US" || loc === "ko-KR" ? loc : "en-US";
  const wholeScoreT =
    scoreLocale === loc ? whole : translateInner(trimmed, scoreLocale);
  let bestText = whole;
  let bestHan = countHanChars(wholeScoreT);
  let bestChanged = whole !== trimmed;
  let bestBothSplit = false;

  for (let i = parts.length - 1; i >= 1; i--) {
    const left = parts.slice(0, i).join(" · ");
    const right = parts.slice(i).join(" · ");
    const leftT = translateInner(left, loc);
    const rightT = translateInner(right, loc);
    const bothChanged = leftT !== left && rightT !== right;
    const anyChanged = leftT !== left || rightT !== right;
    if (!anyChanged) continue;

    const leftScoreT =
      scoreLocale === loc ? leftT : translateInner(left, scoreLocale);
    const rightScoreT =
      scoreLocale === loc ? rightT : translateInner(right, scoreLocale);
    const candHan = countHanChars(leftScoreT) + countHanChars(rightScoreT);
    const candText = `${leftT} · ${rightT}`;

    if (
      candHan < bestHan ||
      (candHan === bestHan && !bestChanged) ||
      (candHan === bestHan &&
        bothChanged &&
        !bestBothSplit &&
        candText !== whole)
    ) {
      bestText = candText;
      bestHan = candHan;
      bestChanged = true;
      bestBothSplit = bothChanged;
    }
  }

  return bestText;
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
    re: /^(模型下载|模型加载|外部 API 对话|外部 API Responses|外部 API 上下文压缩|ComfyUI 启动|ComfyUI 工作流|ComfyUI 任务|音色试听|远端任务)：(进行中|已完成|失败|已取消)(.*)$/s,
    format: (m, loc) => {
      const kind = TASK_KIND_LABELS[m[1]!]?.[loc] ?? m[1]!;
      const rawStatus = m[2]!;
      const status = TASK_STATE_LABELS[rawStatus]?.[loc] ?? rawStatus;
      let rest = (m[3] || "").trim();
      let pctPrefix = "";
      let elapsedPrefix = "";
      const pctMatch = rest.match(/^·\s*(\d+%)(?:\s*·\s*(.*))?$/s);
      if (pctMatch) {
        pctPrefix = ` · ${pctMatch[1]}`;
        rest = (pctMatch[2] || "").trim();
      } else if (rest.startsWith("·")) {
        rest = rest.slice(1).trim();
      }
      const elapsedMatch = rest.match(
        /^(?:·\s*)?耗时\s+([^·]+?)(?:\s*·\s*(.*))?$/s,
      );
      if (elapsedMatch) {
        const dur = translateZhDuration(elapsedMatch[1]!, loc);
        const durPart = {
          "zh-CN": `耗时 ${dur}`,
          "en-US": `took ${dur}`,
          "zh-TW": `耗時 ${dur}`,
          "zh-HK": `耗時 ${dur}`,
          "ja-JP": `所要 ${dur}`,
          "ko-KR": `소요 ${dur}`,
        }[loc];
        elapsedPrefix = ` · ${durPart}`;
        rest = (elapsedMatch[2] || "").trim();
      }
      const tailText = rest
        ? ` · ${translateTaskTail(rest, loc, rawStatus)}`
        : "";
      const colon = loc === "zh-TW" || loc === "zh-HK" ? "：" : ": ";
      return `${kind}${colon}${status}${pctPrefix}${elapsedPrefix}${tailText}`;
    },
  },
  {
    re: /^第 (\d+)\/(\d+) 段 · (.+)$/s,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      const rest = translateInner(m[3]!, loc);
      return `${seg} · ${rest}`;
    },
  },

  // === console/internal/controller/controller.go ===
  {
    re: /^远端已受理 ComfyUI 工作流任务，但刷新状态失败（将自动重试）：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Remote accepted the ComfyUI workflow task, but refreshing status failed (will retry automatically): ${detail}`;
        case "zh-TW":
          return `遠端已受理 ComfyUI 工作流程任務，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "zh-HK":
          return `遠端已受理 ComfyUI 工作流任務，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "ja-JP":
          return `リモートで ComfyUI ワークフロータスクを受け付けましたが、状態の更新に失敗しました（自動で再試行します）: ${detail}`;
        case "ko-KR":
          return `원격에서 ComfyUI 워크플로 작업을 수락했지만 상태 새로고침에 실패했습니다 (자동 재시도 예정): ${detail}`;
        default:
          return m[0];
      }
    },
  },
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
      const name = translateInner(m[1]!, loc);
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
      const name = translateInner(m[1]!, loc);
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
      const wid = translateInner(m[1]!, loc);
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
      const wid = translateInner(m[1]!, loc);
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
    re: /^第 (\d+)\/(\d+) 段 返回的分段视频路径不能是符号链接$/,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${seg} returned a segment video path that cannot be a symbolic link`;
        case "zh-TW":
          return `${seg} 回傳的分段影片路徑不可為符號連結`;
        case "zh-HK":
          return `${seg} 返回的分段影片路徑不能是符號連結`;
        case "ja-JP":
          return `${seg} が返したセグメント動画パスにシンボリックリンクは使用できません`;
        case "ko-KR":
          return `${seg}에서 반환된 세그먼트 영상 경로는 심볼릭 링크일 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s+根目录不能是符号链接$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!, loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} root directory cannot be a symbolic link`;
        case "zh-TW":
          return `${label}${zhSep}根目錄不可為符號連結`;
        case "zh-HK":
          return `${label}${zhSep}根目錄不能是符號連結`;
        case "ja-JP":
          return `${label} のルートディレクトリにシンボリックリンクは使用できません`;
        case "ko-KR":
          return `${label} 루트 디렉터리는 심볼릭 링크일 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
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
    re: /^(模型文件|模型临时文件|过滤后的依赖文件|目标文件)不能是符号链接：(.+)$/,
    format: (m, loc) => {
      const kindMap: Record<string, ColabOneLocaleTable> = {
        模型文件: {
          "zh-CN": "模型文件",
          "en-US": "Model file",
          "zh-TW": "模型檔案",
          "zh-HK": "模型檔案",
          "ja-JP": "モデルファイル",
          "ko-KR": "모델 파일",
        },
        模型临时文件: {
          "zh-CN": "模型临时文件",
          "en-US": "Temporary model file",
          "zh-TW": "模型暫存檔案",
          "zh-HK": "模型臨時檔案",
          "ja-JP": "モデル一時ファイル",
          "ko-KR": "모델 임시 파일",
        },
        过滤后的依赖文件: {
          "zh-CN": "过滤后的依赖文件",
          "en-US": "Filtered requirements file",
          "zh-TW": "過濾後的相依套件檔案",
          "zh-HK": "過濾後的依賴檔案",
          "ja-JP": "フィルタ済み依存関係ファイル",
          "ko-KR": "필터링된 종속성 파일",
        },
        目标文件: {
          "zh-CN": "目标文件",
          "en-US": "Target file",
          "zh-TW": "目標檔案",
          "zh-HK": "目標檔案",
          "ja-JP": "対象ファイル",
          "ko-KR": "대상 파일",
        },
      };
      const label = kindMap[m[1]!]?.[loc] ?? m[1]!;
      const name = m[2]!;
      switch (loc) {
        case "en-US":
          return `${label} cannot be a symbolic link: ${name}`;
        case "zh-TW":
          return `${label}不可為符號連結：${name}`;
        case "zh-HK":
          return `${label}不能是符號連結：${name}`;
        case "ja-JP":
          return `${label}にシンボリックリンクは使用できません: ${name}`;
        case "ko-KR":
          return `${label}은(는) 심볼릭 링크일 수 없습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*不能是符号链接：(.+)$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!, loc);
      const path = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} cannot be a symbolic link: ${path}`;
        case "zh-TW":
          return `${label}${zhSep}不可為符號連結：${path}`;
        case "zh-HK":
          return `${label}${zhSep}不能是符號連結：${path}`;
        case "ja-JP":
          return `${label} にシンボリックリンクは使用できません: ${path}`;
        case "ko-KR":
          return `${label}은(는) 심볼릭 링크일 수 없습니다: ${path}`;
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
    re: /^不支持的(图片|音频)输入文件格式：(.+)$/,
    format: (m, loc) => {
      const isAudio = m[1] === "音频";
      const ext = translateFallbackUnknownWord(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `Unsupported ${isAudio ? "audio" : "image"} input file format: ${ext}`;
        case "zh-TW":
          return `不支援的${isAudio ? "音訊" : "圖片"}輸入檔案格式：${ext}`;
        case "zh-HK":
          return `不支援的${isAudio ? "音訊" : "圖片"}輸入檔案格式：${ext}`;
        case "ja-JP":
          return `サポートされていない${isAudio ? "音声" : "画像"}入力ファイル形式です: ${ext}`;
        case "ko-KR":
          return `지원하지 않는 ${isAudio ? "오디오" : "이미지"} 입력 파일 형식입니다: ${ext}`;
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
    re: /^(?:正在)?提交工作流：(.+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
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
      const name = translateInner(m[1]!, loc);
      const pid = m[2]!;
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
    re: /^获取输出 (\d+)\/(\d+) · (.+)$/,
    format: (m, loc) => {
      const [, idx, total, name] = m;
      switch (loc) {
        case "en-US":
          return `Fetching output ${idx}/${total} · ${name}`;
        case "zh-TW":
          return `取得輸出 ${idx}/${total} · ${name}`;
        case "zh-HK":
          return `獲取輸出 ${idx}/${total} · ${name}`;
        case "ja-JP":
          return `出力を取得中 ${idx}/${total} · ${name}`;
        case "ko-KR":
          return `출력 가져오는 중 ${idx}/${total} · ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流执行完成，未产生可保存的输出文件（耗时 ([^）]+)）$/,
    format: (m, loc) => {
      const dur = translateZhDuration(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Workflow completed with no savable output files (took ${dur})`;
        case "zh-TW":
          return `工作流程執行完成，未產生可儲存的輸出檔案（耗時 ${dur}）`;
        case "zh-HK":
          return `工作流執行完成，未產生可儲存的輸出檔案（耗時 ${dur}）`;
        case "ja-JP":
          return `ワークフロー完了（保存可能な出力ファイルなし・所要 ${dur}）`;
        case "ko-KR":
          return `워크플로 실행 완료, 저장 가능한 출력 파일 없음 (소요 ${dur})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流执行完成，已保存 (\d+) 个作品到远端 GPU(?:（耗时 ([^）]+)）)?$/,
    format: (m, loc) => {
      const n = m[1];
      const rawDur = m[2];
      const dur = rawDur ? translateZhDuration(rawDur, loc) : "";
      switch (loc) {
        case "en-US":
          return dur
            ? `Workflow completed; saved ${n} output(s) to remote GPU (took ${dur})`
            : `Workflow completed; saved ${n} output(s) to remote GPU`;
        case "zh-TW":
          return dur
            ? `工作流程執行完成，已儲存 ${n} 個作品至遠端 GPU（耗時 ${dur}）`
            : `工作流程執行完成，已儲存 ${n} 個作品至遠端 GPU`;
        case "zh-HK":
          return dur
            ? `工作流執行完成，已儲存 ${n} 個作品到遠端 GPU（耗時 ${dur}）`
            : `工作流執行完成，已儲存 ${n} 個作品到遠端 GPU`;
        case "ja-JP":
          return dur
            ? `ワークフロー完了。${n} 件の出力をリモート GPU に保存しました（所要 ${dur}）`
            : `ワークフロー完了。${n} 件の出力をリモート GPU に保存しました`;
        case "ko-KR":
          return dur
            ? `워크플로 실행 완료, 원격 GPU에 결과물 ${n}개 저장됨 (소요 ${dur})`
            : `워크플로 실행 완료, 원격 GPU에 결과물 ${n}개 저장됨`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^排队中（第 (\d+) 位）· 已用 (.+)$/,
    format: (m, loc) => {
      const pos = m[1]!;
      const dur = translateZhDuration(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `Queued (#${pos}) · ${dur} elapsed`;
        case "zh-TW":
          return `排隊中（第 ${pos} 位）· 已用 ${dur}`;
        case "zh-HK":
          return `排隊中（第 ${pos} 位）· 已用 ${dur}`;
        case "ja-JP":
          return `キュー待機中（${pos} 番目）· ${dur}経過`;
        case "ko-KR":
          return `대기 중 (${pos}번째) · ${dur} 경과`;
        default:
          return `排队中（第 ${pos} 位）· 已用 ${dur}`;
      }
    },
  },
  {
    re: /^远端 GPU 执行中 · 已用 (.+)$/,
    format: (m, loc) => {
      const dur = translateZhDuration(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Running on remote GPU · ${dur} elapsed`;
        case "zh-TW":
          return `遠端 GPU 執行中 · 已用 ${dur}`;
        case "zh-HK":
          return `遠端 GPU 執行中 · 已用 ${dur}`;
        case "ja-JP":
          return `リモート GPU で実行中 · ${dur}経過`;
        case "ko-KR":
          return `원격 GPU에서 실행 중 · ${dur} 경과`;
        default:
          return `远端 GPU 执行中 · 已用 ${dur}`;
      }
    },
  },
  {
    re: /^(.+?) · 排队中（第 (\d+) 位）· 已用 (.+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const pos = m[2]!;
      const dur = translateZhDuration(m[3]!, loc);
      switch (loc) {
        case "en-US":
          return `${name} · Queued (#${pos}) · ${dur} elapsed`;
        case "zh-TW":
          return `${name} · 排隊中（第 ${pos} 位）· 已用 ${dur}`;
        case "zh-HK":
          return `${name} · 排隊中（第 ${pos} 位）· 已用 ${dur}`;
        case "ja-JP":
          return `${name} · キュー待機中（${pos} 番目）· ${dur}経過`;
        case "ko-KR":
          return `${name} · 대기 중 (${pos}번째) · ${dur} 경과`;
        default:
          return `${name} · 排队中（第 ${pos} 位）· 已用 ${dur}`;
      }
    },
  },
  {
    re: /^(.+?) · 远端 GPU 执行中 · 已用 (.+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const dur = translateZhDuration(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${name} · Running on remote GPU · ${dur} elapsed`;
        case "zh-TW":
          return `${name} · 遠端 GPU 執行中 · 已用 ${dur}`;
        case "zh-HK":
          return `${name} · 遠端 GPU 執行中 · 已用 ${dur}`;
        case "ja-JP":
          return `${name} · リモート GPU で実行中 · ${dur}経過`;
        case "ko-KR":
          return `${name} · 원격 GPU에서 실행 중 · ${dur} 경과`;
        default:
          return `${name} · 远端 GPU 执行中 · 已用 ${dur}`;
      }
    },
  },
  {
    re: /^(.+?) · 正在远端 GPU 运行 · 已用时 (\d+)s$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const sec = m[2]!;
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
  {
    re: /^(.+?) · 已在 ComfyUI 队列排队$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${name} · Queued in ComfyUI`;
        case "zh-TW":
          return `${name} · 已進入 ComfyUI 佇列`;
        case "zh-HK":
          return `${name} · 已在 ComfyUI 隊列排隊`;
        case "ja-JP":
          return `${name} · ComfyUI キューで待機中`;
        case "ko-KR":
          return `${name} · ComfyUI 대기열에 등록됨`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流 (.+?) 已完成$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Workflow ${name} completed`;
        case "zh-TW":
          return `工作流程 ${name} 已完成`;
        case "zh-HK":
          return `工作流 ${name} 已完成`;
        case "ja-JP":
          return `ワークフロー ${name} が完了しました`;
        case "ko-KR":
          return `워크플로 ${name} 완료됨`;
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
    re: /^Google 云盘导出目录不可读写：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Google Drive export folder is not readable/writable: ${detail}`;
        case "zh-TW":
          return `Google 雲端硬碟匯出目錄無法讀寫：${detail}`;
        case "zh-HK":
          return `Google 雲端硬碟匯出目錄無法讀寫：${detail}`;
        case "ja-JP":
          return `Google ドライブ出力フォルダの読み書きができません: ${detail}`;
        case "ko-KR":
          return `Google 드라이브 내보내기 폴더를 읽고 쓸 수 없습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^写入 Google 云盘失败：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to write to Google Drive: ${detail}`;
        case "zh-TW":
          return `寫入 Google 雲端硬碟失敗：${detail}`;
        case "zh-HK":
          return `寫入 Google 雲端硬碟失敗：${detail}`;
        case "ja-JP":
          return `Google ドライブへの書き込みに失敗しました: ${detail}`;
        case "ko-KR":
          return `Google 드라이브 쓰기 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^导出到 Google 云盘失败：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to export to Google Drive: ${detail}`;
        case "zh-TW":
          return `匯出至 Google 雲端硬碟失敗：${detail}`;
        case "zh-HK":
          return `匯出至 Google 雲端硬碟失敗：${detail}`;
        case "ja-JP":
          return `Google ドライブへの保存に失敗しました: ${detail}`;
        case "ko-KR":
          return `Google 드라이브 내보내기 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已将作品导出至 Google 云盘：My Drive \/ (.+) \/ (.+)$/,
    format: (m, loc) => {
      const folder = m[1]!;
      const name = m[2]!;
      switch (loc) {
        case "en-US":
          return `Exported output to Google Drive: My Drive / ${folder} / ${name}`;
        case "zh-TW":
          return `已將作品匯出至 Google 雲端硬碟：My Drive / ${folder} / ${name}`;
        case "zh-HK":
          return `已將作品匯出至 Google 雲端硬碟：My Drive / ${folder} / ${name}`;
        case "ja-JP":
          return `作品を Google ドライブへ保存しました: My Drive / ${folder} / ${name}`;
        case "ko-KR":
          return `결과물을 Google 드라이브로 저장했습니다: My Drive / ${folder} / ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已将作品导出至 Google 云盘 \((.+)\)$/,
    format: (m, loc) => {
      const folder = m[1]!;
      switch (loc) {
        case "en-US":
          return `Exported output to Google Drive (${folder})`;
        case "zh-TW":
          return `已將作品匯出至 Google 雲端硬碟 (${folder})`;
        case "zh-HK":
          return `已將作品匯出至 Google 雲端硬碟 (${folder})`;
        case "ja-JP":
          return `作品を Google ドライブ (${folder}) へ保存しました`;
        case "ko-KR":
          return `결과물을 Google 드라이브(${folder})로 저장했습니다`;
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
    re: /^检测免费 Drive 配额项目列表失败（(.+)）$/,
    format: (m, loc) => {
      const detail = m[1]!;
      switch (loc) {
        case "en-US":
          return `Failed to list free Drive quota projects (${detail})`;
        case "zh-TW":
          return `偵測免費 Drive 配額專案清單失敗（${detail}）`;
        case "zh-HK":
          return `檢測免費 Drive 配額項目列表失敗（${detail}）`;
        case "ja-JP":
          return `無料の Drive クォータプロジェクト一覧の取得に失敗しました（${detail}）`;
        case "ko-KR":
          return `무료 Drive 할당량 프로젝트 목록 확인 실패(${detail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^自动创建免费 Drive 配额项目 \(([^)]+)\) 失败（(.+)）$/,
    format: (m, loc) => {
      const pid = m[1]!;
      const detail = m[2]!;
      switch (loc) {
        case "en-US":
          return `Failed to auto-create free Drive quota project (${pid}) (${detail})`;
        case "zh-TW":
          return `自動建立免費 Drive 配額專案 (${pid}) 失敗（${detail}）`;
        case "zh-HK":
          return `自動建立免費 Drive 配額項目 (${pid}) 失敗（${detail}）`;
        case "ja-JP":
          return `無料の Drive クォータプロジェクト (${pid}) の自動作成に失敗しました（${detail}）`;
        case "ko-KR":
          return `무료 Drive 할당량 프로젝트(${pid}) 자동 생성 실패(${detail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^等待免费 Drive 配额项目 \(([^)]+)\) 创建完成失败（(.+)）$/,
    format: (m, loc) => {
      const pid = m[1]!;
      const detail = m[2]!;
      switch (loc) {
        case "en-US":
          return `Failed while waiting for free Drive quota project (${pid}) creation (${detail})`;
        case "zh-TW":
          return `等待免費 Drive 配額專案 (${pid}) 建立完成失敗（${detail}）`;
        case "zh-HK":
          return `等待免費 Drive 配額項目 (${pid}) 建立完成失敗（${detail}）`;
        case "ja-JP":
          return `無料の Drive クォータプロジェクト (${pid}) の作成完了待機に失敗しました（${detail}）`;
        case "ko-KR":
          return `무료 Drive 할당량 프로젝트(${pid}) 생성 완료 대기 실패(${detail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^未能为免费 Drive 配额项目 \(([^)]+)\) 启用 Drive API（(.+)）$/,
    format: (m, loc) => {
      const pid = m[1]!;
      const rawDetail = m[2]!;
      const detail = (() => {
        switch (loc) {
          case "en-US":
            return rawDetail.replaceAll("；无配额头回退：", "; without quota header: ");
          case "zh-TW":
            return rawDetail.replaceAll("；无配额头回退：", "；無配額標頭回退：");
          case "zh-HK":
            return rawDetail.replaceAll("；无配额头回退：", "；無配額標頭回退：");
          case "ja-JP":
            return rawDetail.replaceAll("；无配额头回退：", "；クォータヘッダーなし再試行: ");
          case "ko-KR":
            return rawDetail.replaceAll("；无配额头回退：", "; 할당량 헤더 없이 재시도: ");
          default:
            return rawDetail;
        }
      })();
      switch (loc) {
        case "en-US":
          return `Could not enable Drive API for free Drive quota project (${pid}) (${detail})`;
        case "zh-TW":
          return `未能為免費 Drive 配額專案 (${pid}) 啟用 Drive API（${detail}）`;
        case "zh-HK":
          return `未能為免費 Drive 配額項目 (${pid}) 啟用 Drive API（${detail}）`;
        case "ja-JP":
          return `無料の Drive クォータプロジェクト (${pid}) で Drive API を有効化できませんでした（${detail}）`;
        case "ko-KR":
          return `무료 Drive 할당량 프로젝트(${pid})에서 Drive API를 활성화하지 못했습니다(${detail})`;
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
          return `Preparing to download ${name}: ${count} file(s), ${gib} GiB total, ${threads} threads (target: remote GPU disk)`;
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
      const title = translateInner(m[1]!, loc);
      const suffix = m[2]!;
      const mapped = COMFY_FIELD_SUFFIX_LABELS[suffix]?.[loc] ?? suffix;
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
    re: /^非法的 pip 依赖声明[:：]\s*(.+)$/,
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

  // === server/comfy_packs.py ===
  {
    re: /^当前远端 GPU 显存不足：此工作流至少需要 ([\d.]+ (?:GB|GiB|MiB)) 显存，当前 GPU 为 (.+?)(?:，推荐切换至 (.+))?$/,
    format: (m, loc) => {
      const req = m[1]!;
      const cur = translateGpuVramDesc(m[2]!, loc);
      const rec = m[3]?.trim() ?? "";
      switch (loc) {
        case "en-US":
          return `Insufficient remote GPU VRAM: this workflow requires at least ${req} VRAM, current GPU is ${cur}${rec ? `. Recommended GPU: ${rec}` : ""}`;
        case "zh-TW":
          return `目前遠端 GPU VRAM 不足：此工作流程至少需要 ${req} VRAM，目前 GPU 為 ${cur}${rec ? `，建議切換至 ${rec}` : ""}`;
        case "zh-HK":
          return `目前遠端 GPU 顯存不足：此工作流至少需要 ${req} 顯存，目前 GPU 為 ${cur}${rec ? `，建議切換至 ${rec}` : ""}`;
        case "ja-JP":
          return `リモート GPU の VRAM が不足しています。このワークフローには少なくとも ${req} の VRAM が必要ですが、現在の GPU は ${cur} です${rec ? `（推奨 GPU: ${rec}）` : ""}`;
        case "ko-KR":
          return `원격 GPU VRAM이 부족합니다: 이 워크플로는 최소 ${req} VRAM이 필요하며 현재 GPU는 ${cur}입니다${rec ? ` (권장 GPU: ${rec})` : ""}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端磁盘空间不足：至少需要 ([\d.]+) GiB 可用空间（当前仅剩 ([\d.]+) GiB），请清理其他模型或更换磁盘空间更大的远端实例$/,
    format: (m, loc) => {
      const [, req, free] = m;
      switch (loc) {
        case "en-US":
          return `Insufficient remote disk space: requires at least ${req} GiB free (${free} GiB available). Clear other models or switch to a remote instance with more disk space.`;
        case "zh-TW":
          return `遠端磁碟空間不足：至少需要 ${req} GiB 可用空間（目前僅剩 ${free} GiB），請清理其他模型或更換磁碟空間更大的遠端執行個體`;
        case "zh-HK":
          return `遠端磁碟空間不足：至少需要 ${req} GiB 可用空間（目前僅剩 ${free} GiB），請清理其他模型或更換磁碟空間更大的遠端實例`;
        case "ja-JP":
          return `リモートディスク容量が不足しています: 少なくとも ${req} GiB の空き容量が必要です（現在 ${free} GiB 空き）。他のモデルを削除するか、ディスク容量の大きいインスタンスに切り替えてください`;
        case "ko-KR":
          return `원격 디스크 공간이 부족합니다: 최소 ${req} GiB의 여유 공간이 필요합니다(현재 ${free} GiB 남음). 다른 모델을 정리하거나 디스크 용량이 더 큰 원격 인스턴스로 전환하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用远端已就绪的节点包：(.+)$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Reusing ready remote custom node pack: ${wfName}`;
        case "zh-TW":
          return `重複使用遠端已就緒的節點套件：${wfName}`;
        case "zh-HK":
          return `複用遠端已就緒的節點套件：${wfName}`;
        case "ja-JP":
          return `準備済みのリモートノードパックを再利用します: ${wfName}`;
        case "ko-KR":
          return `준비된 원격 노드 팩 재사용 중: ${wfName}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^准备自定义节点 · (.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Preparing custom node · ${name}`;
        case "zh-TW":
          return `準備自訂節點 · ${name}`;
        case "zh-HK":
          return `準備自訂節點 · ${name}`;
        case "ja-JP":
          return `カスタムノードを準備中 · ${name}`;
        case "ko-KR":
          return `사용자 지정 노드 준비 중 · ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在拉取节点包 (.+?)（([^）]+)）$/,
    format: (m, loc) => {
      const [, name, commit] = m;
      switch (loc) {
        case "en-US":
          return `Fetching custom node pack ${name} (${commit})`;
        case "zh-TW":
          return `正在拉取節點套件 ${name}（${commit}）`;
        case "zh-HK":
          return `正在拉取節點套件 ${name}（${commit}）`;
        case "ja-JP":
          return `ノードパック ${name} を取得中（${commit}）`;
        case "ko-KR":
          return `노드 팩 ${name} 가져오는 중 (${commit})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^安装节点依赖 · (.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Installing node dependencies · ${name}`;
        case "zh-TW":
          return `安裝節點相依套件 · ${name}`;
        case "zh-HK":
          return `安裝節點依賴 · ${name}`;
        case "ja-JP":
          return `ノード依存関係をインストール中 · ${name}`;
        case "ko-KR":
          return `노드 종속성 설치 중 · ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在安装 (.+?) 依赖（保留现有 CUDA PyTorch）$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Installing dependencies for ${name} (preserving existing CUDA PyTorch)`;
        case "zh-TW":
          return `正在安裝 ${name} 相依套件（保留現有 CUDA PyTorch）`;
        case "zh-HK":
          return `正在安裝 ${name} 依賴（保留現有 CUDA PyTorch）`;
        case "ja-JP":
          return `${name} の依存関係をインストール中（既存の CUDA PyTorch を保持）`;
        case "ko-KR":
          return `${name} 종속성 설치 중 (기존 CUDA PyTorch 유지)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^安装工作流依赖包 · (.+)$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Installing workflow packages · ${wfName}`;
        case "zh-TW":
          return `安裝工作流程相依套件 · ${wfName}`;
        case "zh-HK":
          return `安裝工作流依賴套件 · ${wfName}`;
        case "ja-JP":
          return `ワークフローパッケージをインストール中 · ${wfName}`;
        case "ko-KR":
          return `워크플로 패키지 설치 중 · ${wfName}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在安装工作流额外依赖（(\d+) 个包）$/,
    format: (m, loc) => {
      const n = m[1]!;
      switch (loc) {
        case "en-US":
          return `Installing extra workflow dependencies (${n} package(s))`;
        case "zh-TW":
          return `正在安裝工作流程額外相依套件（${n} 個套件）`;
        case "zh-HK":
          return `正在安裝工作流額外依賴（${n} 個套件）`;
        case "ja-JP":
          return `ワークフローの追加依存関係をインストール中（${n} パッケージ）`;
        case "ko-KR":
          return `워크플로 추가 종속성 설치 중 (${n}개 패키지)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已应用节点兼容补丁：(.+)$/,
    format: (m, loc) => {
      const label = m[1]!;
      switch (loc) {
        case "en-US":
          return `Applied node compatibility patch: ${label}`;
        case "zh-TW":
          return `已套用節點相容性修補程式：${label}`;
        case "zh-HK":
          return `已套用節點兼容補丁：${label}`;
        case "ja-JP":
          return `ノード互換パッチを適用しました: ${label}`;
        case "ko-KR":
          return `노드 호환 패치를 적용했습니다: ${label}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流节点包准备完成：(.+)$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Workflow custom node pack ready: ${wfName}`;
        case "zh-TW":
          return `工作流程節點套件準備完成：${wfName}`;
        case "zh-HK":
          return `工作流節點套件準備完成：${wfName}`;
        case "ja-JP":
          return `ワークフローのノードパック準備が完了しました: ${wfName}`;
        case "ko-KR":
          return `워크플로 노드 팩 준비 완료: ${wfName}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^自定义节点(目录|符号链接) (.+?) 已存在且非 ColabOne 托管，请手动移走后重试$/,
    format: (m, loc) => {
      const isSymlink = m[1] === "符号链接";
      const name = m[2]!;
      switch (loc) {
        case "en-US":
          return isSymlink
            ? `Custom node symbolic link ${name} already exists and is not managed by ColabOne; move it manually and retry`
            : `Custom node directory ${name} already exists and is not managed by ColabOne; move it manually and retry`;
        case "zh-TW":
          return isSymlink
            ? `自訂節點符號連結 ${name} 已存在且非 ColabOne 託管，請手動移開後重試`
            : `自訂節點目錄 ${name} 已存在且非 ColabOne 託管，請手動移開後重試`;
        case "zh-HK":
          return isSymlink
            ? `自訂節點符號連結 ${name} 已存在且非 ColabOne 託管，請手動移走後重試`
            : `自訂節點目錄 ${name} 已存在且非 ColabOne 託管，請手動移走後重試`;
        case "ja-JP":
          return isSymlink
            ? `カスタムノードのシンボリックリンク ${name} は既に存在し ColabOne 管理対象外です。手動で移動してから再試行してください`
            : `カスタムノードディレクトリ ${name} は既に存在し ColabOne 管理対象外です。手動で移動してから再試行してください`;
        case "ko-KR":
          return isSymlink
            ? `사용자 지정 노드 심볼릭 링크 ${name}이(가) 이미 존재하며 ColabOne에서 관리되지 않습니다. 수동으로 이동한 후 다시 시도하세요`
            : `사용자 지정 노드 디렉터리 ${name}이(가) 이미 존재하며 ColabOne에서 관리되지 않습니다. 수동으로 이동한 후 다시 시도하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流所需的 (\d+) 个模型权重均已校验就绪，跳过下载$/,
    format: (m, loc) => {
      const n = m[1]!;
      switch (loc) {
        case "en-US":
          return `All ${n} required workflow model weight(s) are verified and ready; skipping download`;
        case "zh-TW":
          return `工作流程所需的 ${n} 個模型權重皆已驗證就緒，略過下載`;
        case "zh-HK":
          return `工作流所需的 ${n} 個模型權重均已校驗就緒，跳過下載`;
        case "ja-JP":
          return `ワークフローに必要な ${n} 件のモデル重みはすべて検証済みのためダウンロードをスキップします`;
        case "ko-KR":
          return `워크플로에 필요한 모델 가중치 ${n}개가 모두 검증 완료되어 다운로드를 건너뜁니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^准备下载工作流权重：(\d+) 个文件，共 ([\d.]+) GiB，(\d+) 线程$/,
    format: (m, loc) => {
      const [, count, gib, threads] = m;
      switch (loc) {
        case "en-US":
          return `Preparing to download workflow weights: ${count} file(s), ${gib} GiB total, ${threads} threads`;
        case "zh-TW":
          return `準備下載工作流程權重：${count} 個檔案，共 ${gib} GiB，${threads} 執行緒`;
        case "zh-HK":
          return `準備下載工作流權重：${count} 個檔案，共 ${gib} GiB，${threads} 線程`;
        case "ja-JP":
          return `ワークフローの重みをダウンロード準備中: ${count} ファイル、合計 ${gib} GiB、${threads} スレッド`;
        case "ko-KR":
          return `워크플로 가중치 다운로드 준비 중: ${count}개 파일, 총 ${gib} GiB, ${threads}스레드`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型 (\d+)\/(\d+) 已存在且校验通过，跳过：(.+)$/,
    format: (m, loc) => {
      const [, idx, total, name] = m;
      switch (loc) {
        case "en-US":
          return `Model ${idx}/${total} already exists and passed verification; skipping: ${name}`;
        case "zh-TW":
          return `模型 ${idx}/${total} 已存在且驗證通過，略過：${name}`;
        case "zh-HK":
          return `模型 ${idx}/${total} 已存在且校驗通過，跳過：${name}`;
        case "ja-JP":
          return `モデル ${idx}/${total} は既に検証済みのためスキップします: ${name}`;
        case "ko-KR":
          return `모델 ${idx}/${total}이(가) 이미 존재하며 검증을 통과해 건너뜁니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^发现未通过校验的模型文件，重新下载：(.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Found unverified model file; re-downloading: ${name}`;
        case "zh-TW":
          return `發現未通過驗證的模型檔案，重新下載：${name}`;
        case "zh-HK":
          return `發現未通過校驗的模型檔案，重新下載：${name}`;
        case "ja-JP":
          return `検証未通過のモデルファイルを検出したため再ダウンロードします: ${name}`;
        case "ko-KR":
          return `검증에 통과하지 못한 모델 파일을 발견해 다시 다운로드합니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^下载模型 (\d+)\/(\d+) · (.+)$/,
    format: (m, loc) => {
      const [, idx, total, name] = m;
      switch (loc) {
        case "en-US":
          return `Downloading model ${idx}/${total} · ${name}`;
        case "zh-TW":
          return `下載模型 ${idx}/${total} · ${name}`;
        case "zh-HK":
          return `下載模型 ${idx}/${total} · ${name}`;
        case "ja-JP":
          return `モデルをダウンロード中 ${idx}/${total} · ${name}`;
        case "ko-KR":
          return `모델 다운로드 중 ${idx}/${total} · ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型 (\d+)\/(\d+) · (.+?) · ([\d.]+) GiB$/,
    format: (m, loc) => {
      const [, idx, total, name, gib] = m;
      switch (loc) {
        case "en-US":
          return `Model ${idx}/${total} · ${name} · ${gib} GiB`;
        case "zh-TW":
          return `模型 ${idx}/${total} · ${name} · ${gib} GiB`;
        case "zh-HK":
          return `模型 ${idx}/${total} · ${name} · ${gib} GiB`;
        case "ja-JP":
          return `モデル ${idx}/${total} · ${name} · ${gib} GiB`;
        case "ko-KR":
          return `모델 ${idx}/${total} · ${name} · ${gib} GiB`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型 (\d+)\/(\d+) 传输完成，正在校验 SHA-256$/,
    format: (m, loc) => {
      const [, idx, total] = m;
      switch (loc) {
        case "en-US":
          return `Model ${idx}/${total} transfer complete; verifying SHA-256`;
        case "zh-TW":
          return `模型 ${idx}/${total} 傳輸完成，正在驗證 SHA-256`;
        case "zh-HK":
          return `模型 ${idx}/${total} 傳輸完成，正在校驗 SHA-256`;
        case "ja-JP":
          return `モデル ${idx}/${total} の転送が完了しました。SHA-256 を検証中`;
        case "ko-KR":
          return `모델 ${idx}/${total} 전송 완료, SHA-256 검증 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型 (\d+)\/(\d+) 校验通过，已保存到远端：(.+)$/,
    format: (m, loc) => {
      const [, idx, total, name] = m;
      switch (loc) {
        case "en-US":
          return `Model ${idx}/${total} verified and saved remotely: ${name}`;
        case "zh-TW":
          return `模型 ${idx}/${total} 驗證通過，已儲存至遠端：${name}`;
        case "zh-HK":
          return `模型 ${idx}/${total} 校驗通過，已儲存到遠端：${name}`;
        case "ja-JP":
          return `モデル ${idx}/${total} の検証に合格しリモートに保存しました: ${name}`;
        case "ko-KR":
          return `모델 ${idx}/${total} 검증 통과, 원격에 저장됨: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型文件校验失败：(.+?)，请重新下载$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Model file verification failed: ${name}. Please download again.`;
        case "zh-TW":
          return `模型檔案驗證失敗：${name}，請重新下載`;
        case "zh-HK":
          return `模型檔案校驗失敗：${name}，請重新下載`;
        case "ja-JP":
          return `モデルファイルの検証に失敗しました: ${name}。再ダウンロードしてください`;
        case "ko-KR":
          return `모델 파일 검증 실패: ${name}. 다시 다운로드해 주세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流权重全部已验证：([\d.]+) GiB，用时 ([\d.]+) 秒，平均传输 ([\d.]+) MiB\/s；复用 ([\d.]+) MiB$/,
    format: (m, loc) => {
      const [, gib, sec, speed, reused] = m;
      switch (loc) {
        case "en-US":
          return `All workflow weights verified: ${gib} GiB in ${sec}s, avg ${speed} MiB/s; reused ${reused} MiB`;
        case "zh-TW":
          return `工作流程權重全部驗證完成：${gib} GiB，耗時 ${sec} 秒，平均傳輸 ${speed} MiB/s；重複使用 ${reused} MiB`;
        case "zh-HK":
          return `工作流權重全部已驗證：${gib} GiB，用時 ${sec} 秒，平均傳輸 ${speed} MiB/s；複用 ${reused} MiB`;
        case "ja-JP":
          return `すべてのワークフロー重みを検証しました: ${gib} GiB（所要 ${sec} 秒、平均 ${speed} MiB/s、再利用 ${reused} MiB）`;
        case "ko-KR":
          return `워크플로 가중치 전체 검증 완료: ${gib} GiB, 소요 ${sec}초, 평균 ${speed} MiB/s, 재사용 ${reused} MiB`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型下载鉴权失败（HTTP (\d+)），请在设置中配置有效的 Hugging Face Token$/,
    format: (m, loc) => {
      const code = m[1]!;
      switch (loc) {
        case "en-US":
          return `Model download authentication failed (HTTP ${code}). Please configure a valid Hugging Face Token in Settings.`;
        case "zh-TW":
          return `模型下載驗證失敗（HTTP ${code}），請在設定中配置有效的 Hugging Face Token`;
        case "zh-HK":
          return `模型下載鑑權失敗（HTTP ${code}），請在設定中配置有效的 Hugging Face Token`;
        case "ja-JP":
          return `モデルダウンロードの認証に失敗しました（HTTP ${code}）。設定で有効な Hugging Face Token を構成してください`;
        case "ko-KR":
          return `모델 다운로드 인증 실패 (HTTP ${code}). 설정에서 유효한 Hugging Face 토큰을 구성해 주세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型单连接下载失败（(.+)）$/,
    format: (m, loc) => {
      const reason = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Single-stream model download failed (${reason})`;
        case "zh-TW":
          return `模型單連線下載失敗（${reason}）`;
        case "zh-HK":
          return `模型單連接下載失敗（${reason}）`;
        case "ja-JP":
          return `モデルの単一接続ダウンロードに失敗しました（${reason}）`;
        case "ko-KR":
          return `모델 단일 연결 다운로드 실패 (${reason})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^单连接下载失败（(.+)）$/,
    format: (m, loc) => {
      const reason = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Single-stream download failed (${reason})`;
        case "zh-TW":
          return `單連線下載失敗（${reason}）`;
        case "zh-HK":
          return `單連接下載失敗（${reason}）`;
        case "ja-JP":
          return `単一接続ダウンロードに失敗しました（${reason}）`;
        case "ko-KR":
          return `단일 연결 다운로드 실패 (${reason})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型临时路径不能是目录：(.+)$/,
    format: (m, loc) => {
      const path = m[1]!;
      switch (loc) {
        case "en-US":
          return `Temporary model path cannot be a directory: ${path}`;
        case "zh-TW":
          return `模型暫存路徑不可為目錄：${path}`;
        case "zh-HK":
          return `模型臨時路徑不能是目錄：${path}`;
        case "ja-JP":
          return `モデル一時パスにディレクトリは指定できません: ${path}`;
        case "ko-KR":
          return `모델 임시 경로는 디렉터리일 수 없습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型目标路径已存在同名目录：(.+)$/,
    format: (m, loc) => {
      const path = m[1]!;
      switch (loc) {
        case "en-US":
          return `A directory with the same name already exists at the model target path: ${path}`;
        case "zh-TW":
          return `模型目標路徑已存在同名目錄：${path}`;
        case "zh-HK":
          return `模型目標路徑已存在同名目錄：${path}`;
        case "ja-JP":
          return `モデル保存先パスに同名のディレクトリが既に存在します: ${path}`;
        case "ko-KR":
          return `모델 대상 경로에 같은 이름의 디렉터리가 이미 존재합니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在下载 (.+)$/,
    format: (m, loc) => {
      const target = m[1]!;
      switch (loc) {
        case "en-US":
          return `Downloading ${target}`;
        case "zh-TW":
          return `正在下載 ${target}`;
        case "zh-HK":
          return `正在下載 ${target}`;
        case "ja-JP":
          return `${target} をダウンロード中`;
        case "ko-KR":
          return `${target} 다운로드 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在校验 (.+)$/,
    format: (m, loc) => {
      const target = m[1]!;
      switch (loc) {
        case "en-US":
          return `Verifying ${target}`;
        case "zh-TW":
          return `正在驗證 ${target}`;
        case "zh-HK":
          return `正在校驗 ${target}`;
        case "ja-JP":
          return `${target} を検証中`;
        case "ko-KR":
          return `${target} 검증 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型文件大小不匹配(?:[:：]\s*(.+))?$/,
    format: (m, loc) => {
      const tail = m[1]?.trim() ?? "";
      switch (loc) {
        case "en-US":
          return tail ? `Model file size mismatch: ${tail}` : "Model file size mismatch";
        case "zh-TW":
          return tail ? `模型檔案大小不相符：${tail}` : "模型檔案大小不相符";
        case "zh-HK":
          return tail ? `模型檔案大小不匹配：${tail}` : "模型檔案大小不匹配";
        case "ja-JP":
          return tail ? `モデルファイルサイズが一致しません: ${tail}` : "モデルファイルサイズが一致しません";
        case "ko-KR":
          return tail ? `모델 파일 크기가 일치하지 않습니다: ${tail}` : "모델 파일 크기가 일치하지 않습니다";
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型 SHA-256 校验失败(?:[:：]\s*(.+))?$/,
    format: (m, loc) => {
      const tail = m[1]?.trim() ?? "";
      switch (loc) {
        case "en-US":
          return tail ? `Model SHA-256 verification failed: ${tail}` : "Model SHA-256 verification failed";
        case "zh-TW":
          return tail ? `模型 SHA-256 驗證失敗：${tail}` : "模型 SHA-256 驗證失敗";
        case "zh-HK":
          return tail ? `模型 SHA-256 校驗失敗：${tail}` : "模型 SHA-256 校驗失敗";
        case "ja-JP":
          return tail ? `モデルの SHA-256 検証に失敗しました: ${tail}` : "モデルの SHA-256 検証に失敗しました";
        case "ko-KR":
          return tail ? `모델 SHA-256 검증 실패: ${tail}` : "모델 SHA-256 검증 실패";
        default:
          return m[0];
      }
    },
  },
  {
    re: /^下载模型失败(?:[:：]\s*(.+))?$/s,
    format: (m, loc) => {
      const detail = m[1] ? translateInner(m[1], loc) : "";
      switch (loc) {
        case "en-US":
          return detail ? `Failed to download model: ${detail}` : "Failed to download model";
        case "zh-TW":
          return detail ? `下載模型失敗：${detail}` : "下載模型失敗";
        case "zh-HK":
          return detail ? `下載模型失敗：${detail}` : "下載模型失敗";
        case "ja-JP":
          return detail ? `モデルのダウンロードに失敗しました: ${detail}` : "モデルのダウンロードに失敗しました";
        case "ko-KR":
          return detail ? `모델 다운로드 실패: ${detail}` : "모델 다운로드 실패";
        default:
          return m[0];
      }
    },
  },
  {
    re: /^期望 dict 或模型对象，实际为 (.+)$/,
    format: (m, loc) => {
      const actual = m[1]!;
      switch (loc) {
        case "en-US":
          return `Expected dict or model object, got ${actual}`;
        case "zh-TW":
          return `預期 dict 或模型物件，實際為 ${actual}`;
        case "zh-HK":
          return `期望 dict 或模型物件，實際為 ${actual}`;
        case "ja-JP":
          return `dict またはモデルオブジェクトが必要ですが、${actual} を受け取りました`;
        case "ko-KR":
          return `dict 또는 모델 객체가 필요하지만 ${actual}을(를) 받았습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^自定义节点名称不合法：(.+)$/,
    format: (m, loc) => {
      const val = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid custom node name: ${val}`;
        case "zh-TW":
          return `自訂節點名稱無效：${val}`;
        case "zh-HK":
          return `自訂節點名稱無效：${val}`;
        case "ja-JP":
          return `無効なカスタムノード名: ${val}`;
        case "ko-KR":
          return `유효하지 않은 사용자 지정 노드 이름: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(提交哈希|模型修订版本|节点 .+? 提交哈希)必须为 40 位十六进制 SHA：(.+)$/,
    format: (m, loc) => {
      const label = translateCommitLabel(m[1]!, loc);
      const val = m[2]!;
      switch (loc) {
        case "en-US":
          return `${label} must be a 40-character hexadecimal SHA: ${val}`;
        case "zh-TW":
          return `${label}必須為 40 位十六進位 SHA：${val}`;
        case "zh-HK":
          return `${label}必須為 40 位十六進位 SHA：${val}`;
        case "ja-JP":
          return `${label}は 40 桁の 16 進 SHA である必要があります: ${val}`;
        case "ko-KR":
          return `${label}은(는) 40자리 16진수 SHA여야 합니다: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(提交哈希|模型修订版本|节点 .+? 提交哈希)不合法$/,
    format: (m, loc) => {
      const label = translateCommitLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Invalid ${label.toLowerCase()}`;
        case "zh-TW":
          return `${label}無效`;
        case "zh-HK":
          return `${label}無效`;
        case "ja-JP":
          return `${label}が無効です`;
        case "ko-KR":
          return `유효하지 않은 ${label}입니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^自定义节点仓库地址不合法：(.+)$/,
    format: (m, loc) => {
      const val = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid custom node repository URL: ${val}`;
        case "zh-TW":
          return `自訂節點儲存庫網址無效：${val}`;
        case "zh-HK":
          return `自訂節點倉庫地址無效：${val}`;
        case "ja-JP":
          return `無効なカスタムノードリポジトリ URL: ${val}`;
        case "ko-KR":
          return `유효하지 않은 사용자 지정 노드 저장소 URL: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型仓库地址不合法：(.+)$/,
    format: (m, loc) => {
      const val = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid model repository address: ${val}`;
        case "zh-TW":
          return `模型儲存庫位址無效：${val}`;
        case "zh-HK":
          return `模型倉庫地址無效：${val}`;
        case "ja-JP":
          return `無効なモデルリポジトリ指定: ${val}`;
        case "ko-KR":
          return `유효하지 않은 모델 저장소 주소: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^无法解析 multitalk\/nodes\.py：(.+)$/s,
    format: (m, loc) => {
      const detail = m[1]!;
      switch (loc) {
        case "en-US":
          return `Failed to parse multitalk/nodes.py: ${detail}`;
        case "zh-TW":
          return `無法解析 multitalk/nodes.py：${detail}`;
        case "zh-HK":
          return `無法解析 multitalk/nodes.py：${detail}`;
        case "ja-JP":
          return `multitalk/nodes.py の解析に失敗しました: ${detail}`;
        case "ko-KR":
          return `multitalk/nodes.py 파싱 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^应用 wav2vec2 兼容补丁后语法校验失败：(.+)$/s,
    format: (m, loc) => {
      const detail = m[1]!;
      switch (loc) {
        case "en-US":
          return `Syntax check failed after applying wav2vec2 compatibility patch: ${detail}`;
        case "zh-TW":
          return `套用 wav2vec2 相容性修補程式後語法驗證失敗：${detail}`;
        case "zh-HK":
          return `套用 wav2vec2 兼容補丁後語法校驗失敗：${detail}`;
        case "ja-JP":
          return `wav2vec2 互換パッチ適用後の構文検証に失敗しました: ${detail}`;
        case "ko-KR":
          return `wav2vec2 호환 패치 적용 후 구문 검사 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的模型子目录：(.+)$/,
    format: (m, loc) => {
      const dir = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unsupported model subdirectory: ${dir}`;
        case "zh-TW":
          return `不支援的模型子目錄：${dir}`;
        case "zh-HK":
          return `不支援的模型子目錄：${dir}`;
        case "ja-JP":
          return `サポートされていないモデルサブディレクトリ: ${dir}`;
        case "ko-KR":
          return `지원하지 않는 모델 하위 디렉터리: ${dir}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型文件路径不合法：(.+)$/,
    format: (m, loc) => {
      const val = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid model file path: ${val}`;
        case "zh-TW":
          return `模型檔案路徑無效：${val}`;
        case "zh-HK":
          return `模型檔案路徑無效：${val}`;
        case "ja-JP":
          return `無効なモデルファイルパス: ${val}`;
        case "ko-KR":
          return `유효하지 않은 모델 파일 경로: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型文件名不合法：(.+)$/,
    format: (m, loc) => {
      const val = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid model filename: ${val}`;
        case "zh-TW":
          return `模型檔案名稱無效：${val}`;
        case "zh-HK":
          return `模型檔案名稱無效：${val}`;
        case "ja-JP":
          return `無効なモデルファイル名: ${val}`;
        case "ko-KR":
          return `유효하지 않은 모델 파일 이름: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^存在同路径但校验信息冲突的模型声明：(.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Conflicting verification metadata for duplicate model path: ${name}`;
        case "zh-TW":
          return `存在相同路徑但驗證資訊衝突的模型宣告：${name}`;
        case "zh-HK":
          return `存在同路徑但校驗資訊衝突的模型聲明：${name}`;
        case "ja-JP":
          return `同一パスで検証情報が競合するモデル定義があります: ${name}`;
        case "ko-KR":
          return `동일 경로에 검증 정보가 충돌하는 모델 선언이 있습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不允许的自定义节点补丁类型：(.+)$/,
    format: (m, loc) => {
      const kind = m[1]!;
      switch (loc) {
        case "en-US":
          return `Disallowed custom node patch type: ${kind}`;
        case "zh-TW":
          return `不允許的自訂節點修補類型：${kind}`;
        case "zh-HK":
          return `不允許的自訂節點補丁類型：${kind}`;
        case "ja-JP":
          return `許可されていないカスタムノードパッチ種別: ${kind}`;
        case "ko-KR":
          return `허용되지 않는 사용자 지정 노드 패치 유형: ${kind}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^节点补丁目标文件不存在：(.+)$/,
    format: (m, loc) => {
      const file = m[1]!;
      switch (loc) {
        case "en-US":
          return `Node patch target file does not exist: ${file}`;
        case "zh-TW":
          return `節點修補目標檔案不存在：${file}`;
        case "zh-HK":
          return `節點補丁目標檔案不存在：${file}`;
        case "ja-JP":
          return `ノードパッチの対象ファイルが見つかりません: ${file}`;
        case "ko-KR":
          return `노드 패치 대상 파일이 존재하지 않습니다: ${file}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^自定义节点名称重复：(.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Duplicate custom node name: ${name}`;
        case "zh-TW":
          return `自訂節點名稱重複：${name}`;
        case "zh-HK":
          return `自訂節點名稱重複：${name}`;
        case "ja-JP":
          return `カスタムノード名が重複しています: ${name}`;
        case "ko-KR":
          return `사용자 지정 노드 이름이 중복되었습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型校验元数据不合法：(.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid model verification metadata: ${name}`;
        case "zh-TW":
          return `模型驗證中繼資料無效：${name}`;
        case "zh-HK":
          return `模型校驗元數據無效：${name}`;
        case "ja-JP":
          return `モデルの検証メタデータが無効です: ${name}`;
        case "ko-KR":
          return `유효하지 않은 모델 검증 메타데이터: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流编号不合法：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid workflow ID: ${id}`;
        case "zh-TW":
          return `工作流程編號無效：${id}`;
        case "zh-HK":
          return `工作流編號無效：${id}`;
        case "ja-JP":
          return `無効なワークフロー ID: ${id}`;
        case "ko-KR":
          return `유효하지 않은 워크플로 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },

  // === server/tts_engines.py ===
  {
    re: /^未检测到可用远端 GPU，(.+?) 需要至少 (\d+) MiB 显存$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      const mib = m[2]!;
      switch (loc) {
        case "en-US":
          return `No available remote GPU detected; ${label} requires at least ${mib} MiB VRAM`;
        case "zh-TW":
          return `未偵測到可用遠端 GPU，${label} 需要至少 ${mib} MiB VRAM`;
        case "zh-HK":
          return `未檢測到可用遠端 GPU，${label} 需要至少 ${mib} MiB 顯存`;
        case "ja-JP":
          return `利用可能なリモート GPU が検出されません。${label} には少なくとも ${mib} MiB の VRAM が必要です`;
        case "ko-KR":
          return `사용 가능한 원격 GPU가 감지되지 않았습니다. ${label}에는 최소 ${mib} MiB VRAM이 필요합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 TTS 引擎 (.+?) 需要至少 (\d+) MiB 显存，当前仅 (\d+) MiB$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      const req = m[2]!;
      const avail = m[3]!;
      switch (loc) {
        case "en-US":
          return `Remote TTS engine ${label} requires at least ${req} MiB VRAM, but only ${avail} MiB is available`;
        case "zh-TW":
          return `遠端 TTS 引擎 ${label} 需要至少 ${req} MiB VRAM，目前僅 ${avail} MiB`;
        case "zh-HK":
          return `遠端 TTS 引擎 ${label} 需要至少 ${req} MiB 顯存，目前僅 ${avail} MiB`;
        case "ja-JP":
          return `リモート TTS エンジン ${label} には少なくとも ${req} MiB の VRAM が必要ですが、現在は ${avail} MiB のみです`;
        case "ko-KR":
          return `원격 TTS 엔진 ${label}은(는) 최소 ${req} MiB VRAM이 필요하지만 현재 ${avail} MiB만 사용 가능합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^未知的远端 TTS 引擎：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unknown remote TTS engine: ${id}`;
        case "zh-TW":
          return `未知的遠端 TTS 引擎：${id}`;
        case "zh-HK":
          return `未知的遠端 TTS 引擎：${id}`;
        case "ja-JP":
          return `不明なリモート TTS エンジン: ${id}`;
        case "ko-KR":
          return `알 수 없는 원격 TTS 엔진: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法的 TTS 引擎编号：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid TTS engine ID: ${id}`;
        case "zh-TW":
          return `無效的 TTS 引擎編號：${id}`;
        case "zh-HK":
          return `無效的 TTS 引擎編號：${id}`;
        case "ja-JP":
          return `無効な TTS エンジン ID: ${id}`;
        case "ko-KR":
          return `유효하지 않은 TTS 엔진 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用已就绪的远端 TTS 引擎环境：(.+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Reusing ready remote TTS engine environment: ${name}`;
        case "zh-TW":
          return `重複使用已就緒的遠端 TTS 引擎環境：${name}`;
        case "zh-HK":
          return `複用已就緒的遠端 TTS 引擎環境：${name}`;
        case "ja-JP":
          return `準備済みのリモート TTS エンジン環境を再利用します: ${name}`;
        case "ko-KR":
          return `준비된 원격 TTS 엔진 환경 재사용 중: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在安装远端 TTS 引擎依赖：(.+?)（共 (\d+) 个包，保留现有 PyTorch）$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const n = m[2]!;
      switch (loc) {
        case "en-US":
          return `Installing remote TTS engine dependencies: ${name} (${n} package(s), preserving existing PyTorch)`;
        case "zh-TW":
          return `正在安裝遠端 TTS 引擎相依套件：${name}（共 ${n} 個套件，保留現有 PyTorch）`;
        case "zh-HK":
          return `正在安裝遠端 TTS 引擎依賴：${name}（共 ${n} 個套件，保留現有 PyTorch）`;
        case "ja-JP":
          return `リモート TTS エンジンの依存関係をインストール中: ${name}（計 ${n} パッケージ、既存 PyTorch を保持）`;
        case "ko-KR":
          return `원격 TTS 엔진 종속성 설치 중: ${name} (총 ${n}개 패키지, 기존 PyTorch 유지)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端缺少 Git，无法拉取 TTS 引擎源码：(.+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Git is missing on the remote runtime; cannot fetch TTS engine source: ${name}`;
        case "zh-TW":
          return `遠端缺少 Git，無法拉取 TTS 引擎原始碼：${name}`;
        case "zh-HK":
          return `遠端缺少 Git，無法拉取 TTS 引擎源碼：${name}`;
        case "ja-JP":
          return `リモート環境に Git がないため TTS エンジンのソースを取得できません: ${name}`;
        case "ko-KR":
          return `원격 런타임에 Git이 없어 TTS 엔진 소스를 가져올 수 없습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在拉取远端 TTS 引擎源码：(.+?)@([a-fA-F0-9]+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const rev = m[2]!;
      switch (loc) {
        case "en-US":
          return `Fetching remote TTS engine source: ${name}@${rev}`;
        case "zh-TW":
          return `正在拉取遠端 TTS 引擎原始碼：${name}@${rev}`;
        case "zh-HK":
          return `正在拉取遠端 TTS 引擎源碼：${name}@${rev}`;
        case "ja-JP":
          return `リモート TTS エンジンのソースを取得中: ${name}@${rev}`;
        case "ko-KR":
          return `원격 TTS 엔진 소스 가져오는 중: ${name}@${rev}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在安装 (.+?) 源码依赖（保留现有 PyTorch）$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Installing source dependencies for ${name} (preserving existing PyTorch)`;
        case "zh-TW":
          return `正在安裝 ${name} 原始碼相依套件（保留現有 PyTorch）`;
        case "zh-HK":
          return `正在安裝 ${name} 源碼依賴（保留現有 PyTorch）`;
        case "ja-JP":
          return `${name} のソース依存関係をインストール中（既存 PyTorch を保持）`;
        case "ko-KR":
          return `${name} 소스 종속성 설치 중 (기존 PyTorch 유지)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 TTS 引擎环境就绪：(.+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Remote TTS engine environment ready: ${name}`;
        case "zh-TW":
          return `遠端 TTS 引擎環境已就緒：${name}`;
        case "zh-HK":
          return `遠端 TTS 引擎環境就緒：${name}`;
        case "ja-JP":
          return `リモート TTS エンジン環境の準備が完了しました: ${name}`;
        case "ko-KR":
          return `원격 TTS 엔진 환경 준비 완료: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在准备远端 TTS 环境(?:[:：]\s*(.+)|（(.+)）)?(?:\.{3}|…)?$/,
    format: (m, loc) => {
      const rawName = (m[1] || m[2] || "").trim();
      const name = rawName ? translateInner(rawName, loc) : "";
      switch (loc) {
        case "en-US":
          return name
            ? `Preparing remote TTS environment: ${name}`
            : "Preparing remote TTS environment";
        case "zh-TW":
          return name
            ? `正在準備遠端 TTS 環境：${name}`
            : "正在準備遠端 TTS 環境";
        case "zh-HK":
          return name
            ? `正在準備遠端 TTS 環境：${name}`
            : "正在準備遠端 TTS 環境";
        case "ja-JP":
          return name
            ? `リモート TTS 環境を準備中: ${name}`
            : "リモート TTS 環境を準備中";
        case "ko-KR":
          return name
            ? `원격 TTS 환경 준비 중: ${name}`
            : "원격 TTS 환경 준비 중";
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在创建远端 TTS 独立虚拟环境：(.+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Creating isolated remote TTS virtual environment: ${name}`;
        case "zh-TW":
          return `正在建立遠端 TTS 獨立虛擬環境：${name}`;
        case "zh-HK":
          return `正在建立遠端 TTS 獨立虛擬環境：${name}`;
        case "ja-JP":
          return `リモート TTS 用の独立仮想環境を作成中: ${name}`;
        case "ko-KR":
          return `원격 TTS 독립 가상 환경 생성 중: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在安装 (.+?) 依赖(?:\.{3}|…)?$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Installing ${name} dependencies`;
        case "zh-TW":
          return `正在安裝 ${name} 相依套件`;
        case "zh-HK":
          return `正在安裝 ${name} 依賴`;
        case "ja-JP":
          return `${name} の依存関係をインストール中`;
        case "ko-KR":
          return `${name} 종속성 설치 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在远端合成语音(?:\.{3}|…)?$/,
    format: (m, loc) => {
      switch (loc) {
        case "en-US":
          return "Synthesizing speech remotely";
        case "zh-TW":
          return "正在遠端合成語音";
        case "zh-HK":
          return "正在遠端合成語音";
        case "ja-JP":
          return "リモートで音声を合成中";
        case "ko-KR":
          return "원격에서 음성 합성 중";
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已完成第 (\d+)\/(\d+) 句合成(?:\.{3}|…)?$/,
    format: (m, loc) => {
      const [, cur, total] = m;
      switch (loc) {
        case "en-US":
          return `Synthesized sentence ${cur}/${total}`;
        case "zh-TW":
          return `已完成第 ${cur}/${total} 句合成`;
        case "zh-HK":
          return `已完成第 ${cur}/${total} 句合成`;
        case "ja-JP":
          return `${cur}/${total} 文目の合成が完了しました`;
        case "ko-KR":
          return `${cur}/${total}번째 문장 합성 완료`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在合成并标准化 48\s*kHz 母带音频(?:\.{3}|…)?$/,
    format: (m, loc) => {
      switch (loc) {
        case "en-US":
          return "Assembling and normalizing 48 kHz master audio";
        case "zh-TW":
          return "正在合成並標準化 48 kHz 母帶音訊";
        case "zh-HK":
          return "正在合成並標準化 48 kHz 母帶音訊";
        case "ja-JP":
          return "48 kHz マスター音声を結合・正規化中";
        case "ko-KR":
          return "48 kHz 마스터 오디오 합성 및 정규화 중";
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非商用许可提示：当前 TTS 引擎「(.+?)」采用 (.+?)(?: — (.+))?$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const lic = m[2]!;
      const notice = m[3] ? translateInner(m[3], loc) : "";
      switch (loc) {
        case "en-US":
          return `Non-commercial license notice: TTS engine "${name}" uses ${lic}${notice ? ` - ${notice}` : ""}`;
        case "zh-TW":
          return `非商用授權提示：目前 TTS 引擎「${name}」採用 ${lic}${notice ? ` — ${notice}` : ""}`;
        case "zh-HK":
          return `非商用許可提示：目前 TTS 引擎「${name}」採用 ${lic}${notice ? ` — ${notice}` : ""}`;
        case "ja-JP":
          return `非商用ライセンス通知: TTS エンジン「${name}」は ${lic} を採用しています${notice ? ` — ${notice}` : ""}`;
        case "ko-KR":
          return `비상업 라이선스 안내: TTS 엔진 "${name}"은(는) ${lic} 라이선스를 사용합니다${notice ? ` - ${notice}` : ""}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^开始远端 TTS 合成：(.+?)（共 (\d+) 段）$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const count = m[2]!;
      switch (loc) {
        case "en-US":
          return `Starting remote TTS synthesis: ${name} (${count} segment(s))`;
        case "zh-TW":
          return `開始遠端 TTS 合成：${name}（共 ${count} 段）`;
        case "zh-HK":
          return `開始遠端 TTS 合成：${name}（共 ${count} 段）`;
        case "ja-JP":
          return `リモート TTS 合成を開始します: ${name}（計 ${count} セグメント）`;
        case "ko-KR":
          return `원격 TTS 합성 시작: ${name} (총 ${count}개 구간)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 TTS 合成完成：(.+?)（总时长 ([\d.]+) 秒，(\d+) Hz）$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const dur = m[2]!;
      const sr = m[3]!;
      switch (loc) {
        case "en-US":
          return `Remote TTS synthesis completed: ${name} (duration ${dur}s, ${sr} Hz)`;
        case "zh-TW":
          return `遠端 TTS 合成完成：${name}（總時長 ${dur} 秒，${sr} Hz）`;
        case "zh-HK":
          return `遠端 TTS 合成完成：${name}（總時長 ${dur} 秒，${sr} Hz）`;
        case "ja-JP":
          return `リモート TTS 合成が完了しました: ${name}（合計 ${dur} 秒、${sr} Hz）`;
        case "ko-KR":
          return `원격 TTS 합성 완료: ${name} (총 길이 ${dur}초, ${sr} Hz)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 TTS 执行失败（退出码 (-?\d+)）(?:：(.*))?$/s,
    format: (m, loc) => {
      const code = m[1]!;
      const tailRaw = (m[2] ?? "").trim();
      const tail = tailRaw ? translateInner(tailRaw, loc) : "";
      switch (loc) {
        case "en-US":
          return `Remote TTS execution failed (exit code ${code})${tail ? `: ${tail}` : ""}`;
        case "zh-TW":
          return `遠端 TTS 執行失敗（結束代碼 ${code}）${tail ? `：${tail}` : ""}`;
        case "zh-HK":
          return `遠端 TTS 執行失敗（退出碼 ${code}）${tail ? `：${tail}` : ""}`;
        case "ja-JP":
          return `リモート TTS の実行に失敗しました（終了コード ${code}）${tail ? `: ${tail}` : ""}`;
        case "ko-KR":
          return `원격 TTS 실행 실패 (종료 코드 ${code})${tail ? `: ${tail}` : ""}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^TTS 推理进程异常退出（退出码 (-?\d+)）(?:：(.*))?$/s,
    format: (m, loc) => {
      const code = m[1]!;
      const tailRaw = (m[2] ?? "").trim();
      const tail = tailRaw ? translateInner(tailRaw, loc) : "";
      switch (loc) {
        case "en-US":
          return `TTS inference process exited unexpectedly (exit code ${code})${tail ? `: ${tail}` : ""}`;
        case "zh-TW":
          return `TTS 推論處理程序異常結束（結束代碼 ${code}）${tail ? `：${tail}` : ""}`;
        case "zh-HK":
          return `TTS 推理進程異常退出（退出碼 ${code}）${tail ? `：${tail}` : ""}`;
        case "ja-JP":
          return `TTS 推論プロセスが異常終了しました（終了コード ${code}）${tail ? `: ${tail}` : ""}`;
        case "ko-KR":
          return `TTS 추론 프로세스가 비정상 종료되었습니다 (종료 코드 ${code})${tail ? `: ${tail}` : ""}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 TTS 引擎 (.+?) 未生成第 (\d+) 段音频：(.+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const idx = m[2]!;
      const path = m[3]!;
      switch (loc) {
        case "en-US":
          return `Remote TTS engine ${name} did not produce audio segment ${idx}: ${path}`;
        case "zh-TW":
          return `遠端 TTS 引擎 ${name} 未產生第 ${idx} 段音訊：${path}`;
        case "zh-HK":
          return `遠端 TTS 引擎 ${name} 未生成第 ${idx} 段音訊：${path}`;
        case "ja-JP":
          return `リモート TTS エンジン ${name} が第 ${idx} セグメント音声を生成しませんでした: ${path}`;
        case "ko-KR":
          return `원격 TTS 엔진 ${name}이(가) ${idx}번째 오디오 구간을 생성하지 못했습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的 TTS 引擎脚本：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unsupported TTS engine worker script: ${id}`;
        case "zh-TW":
          return `不支援的 TTS 引擎指令碼：${id}`;
        case "zh-HK":
          return `不支援的 TTS 引擎腳本：${id}`;
        case "ja-JP":
          return `サポートされていない TTS エンジンスクリプト: ${id}`;
        case "ko-KR":
          return `지원하지 않는 TTS 엔진 스크립트: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^synth_fn 返回了不支持的结果类型：(.+)$/,
    format: (m, loc) => {
      const typeName = m[1]!;
      switch (loc) {
        case "en-US":
          return `synth_fn returned an unsupported result type: ${typeName}`;
        case "zh-TW":
          return `synth_fn 回傳了不支援的結果類型：${typeName}`;
        case "zh-HK":
          return `synth_fn 返回了不支援的結果類型：${typeName}`;
        case "ja-JP":
          return `synth_fn がサポートされていない結果型を返しました: ${typeName}`;
        case "ko-KR":
          return `synth_fn이 지원하지 않는 결과 유형을 반환했습니다: ${typeName}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^音色参考音频文件不存在：(.+)$/,
    format: (m, loc) => {
      const path = m[1]!;
      switch (loc) {
        case "en-US":
          return `Reference voice audio file does not exist: ${path}`;
        case "zh-TW":
          return `音色參考音訊檔案不存在：${path}`;
        case "zh-HK":
          return `聲線參考音訊檔案不存在：${path}`;
        case "ja-JP":
          return `参照音声ファイルが見つかりません: ${path}`;
        case "ko-KR":
          return `참조 음성 오디오 파일이 존재하지 않습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^情绪参考音频文件不存在：(.+)$/,
    format: (m, loc) => {
      const path = m[1]!;
      switch (loc) {
        case "en-US":
          return `Emotion reference audio file does not exist: ${path}`;
        case "zh-TW":
          return `情緒參考音訊檔案不存在：${path}`;
        case "zh-HK":
          return `情緒參考音訊檔案不存在：${path}`;
        case "ja-JP":
          return `感情参照音声ファイルが見つかりません: ${path}`;
        case "ko-KR":
          return `감정 참조 오디오 파일이 존재하지 않습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^无效的 WAV 文件头：(.+)$/,
    format: (m, loc) => {
      const path = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid WAV file header: ${path}`;
        case "zh-TW":
          return `無效的 WAV 檔頭：${path}`;
        case "zh-HK":
          return `無效的 WAV 檔案頭：${path}`;
        case "ja-JP":
          return `無効な WAV ファイルヘッダー: ${path}`;
        case "ko-KR":
          return `유효하지 않은 WAV 파일 헤더: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的 WAV 编码格式：(.+)$/,
    format: (m, loc) => {
      const path = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unsupported WAV encoding format: ${path}`;
        case "zh-TW":
          return `不支援的 WAV 編碼格式：${path}`;
        case "zh-HK":
          return `不支援的 WAV 編碼格式：${path}`;
        case "ja-JP":
          return `サポートされていない WAV エンコード形式: ${path}`;
        case "ko-KR":
          return `지원하지 않는 WAV 인코딩 형식: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的浮点 WAV 位深：(.+)$/,
    format: (m, loc) => {
      const bits = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unsupported floating-point WAV bit depth: ${bits}`;
        case "zh-TW":
          return `不支援的浮點 WAV 位元深度：${bits}`;
        case "zh-HK":
          return `不支援的浮點 WAV 位深：${bits}`;
        case "ja-JP":
          return `サポートされていない浮動小数点 WAV ビット深度: ${bits}`;
        case "ko-KR":
          return `지원하지 않는 부동소수점 WAV 비트 깊이: ${bits}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^WAV 文件不存在：(.+)$/,
    format: (m, loc) => {
      const path = m[1]!;
      switch (loc) {
        case "en-US":
          return `WAV file does not exist: ${path}`;
        case "zh-TW":
          return `WAV 檔案不存在：${path}`;
        case "zh-HK":
          return `WAV 檔案不存在：${path}`;
        case "ja-JP":
          return `WAV ファイルが見つかりません: ${path}`;
        case "ko-KR":
          return `WAV 파일이 존재하지 않습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^WAV 采样率无效：(.+)$/,
    format: (m, loc) => {
      const sr = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid WAV sample rate: ${sr}`;
        case "zh-TW":
          return `WAV 取樣率無效：${sr}`;
        case "zh-HK":
          return `WAV 採樣率無效：${sr}`;
        case "ja-JP":
          return `無効な WAV サンプリングレート: ${sr}`;
        case "ko-KR":
          return `유효하지 않은 WAV 샘플링 레이트: ${sr}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的 PCM 位宽：(.+?) 字节$/,
    format: (m, loc) => {
      const width = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unsupported PCM sample width: ${width} byte(s)`;
        case "zh-TW":
          return `不支援的 PCM 位元寬度：${width} 位元組`;
        case "zh-HK":
          return `不支援的 PCM 位寬：${width} 位元組`;
        case "ja-JP":
          return `サポートされていない PCM サンプル幅: ${width} バイト`;
        case "ko-KR":
          return `지원하지 않는 PCM 샘플 너비: ${width}바이트`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(目标)?采样率必须大于 0：(.+)$/,
    format: (m, loc) => {
      const isTarget = Boolean(m[1]);
      const val = m[2]!;
      switch (loc) {
        case "en-US":
          return `${isTarget ? "Target sample rate" : "Sample rate"} must be greater than 0: ${val}`;
        case "zh-TW":
          return `${isTarget ? "目標取樣率" : "取樣率"}必須大於 0：${val}`;
        case "zh-HK":
          return `${isTarget ? "目標採樣率" : "採樣率"}必須大於 0：${val}`;
        case "ja-JP":
          return `${isTarget ? "目標サンプリングレート" : "サンプリングレート"}は 0 より大きい必要があります: ${val}`;
        case "ko-KR":
          return `${isTarget ? "목표 샘플링 레이트" : "샘플링 레이트"}는 0보다 커야 합니다: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^在 (.+?) 中未找到 Fish Speech 解码器权重文件$/,
    format: (m, loc) => {
      const dir = m[1]!;
      switch (loc) {
        case "en-US":
          return `Fish Speech decoder checkpoint was not found in ${dir}`;
        case "zh-TW":
          return `在 ${dir} 中找不到 Fish Speech 解碼器權重檔案`;
        case "zh-HK":
          return `在 ${dir} 中未找到 Fish Speech 解碼器權重檔案`;
        case "ja-JP":
          return `${dir} に Fish Speech デコーダーの重みファイルが見つかりません`;
        case "ko-KR":
          return `${dir}에서 Fish Speech 디코더 가중치 파일을 찾을 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Fish Speech S2 Pro 推理失败：(.+)$/s,
    format: (m, loc) => {
      const detail = m[1]!;
      switch (loc) {
        case "en-US":
          return `Fish Speech S2 Pro inference failed: ${detail}`;
        case "zh-TW":
          return `Fish Speech S2 Pro 推論失敗：${detail}`;
        case "zh-HK":
          return `Fish Speech S2 Pro 推理失敗：${detail}`;
        case "ja-JP":
          return `Fish Speech S2 Pro の推論に失敗しました: ${detail}`;
        case "ko-KR":
          return `Fish Speech S2 Pro 추론 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Fish Speech S2 Pro 未返回第 (\d+) 段音频$/,
    format: (m, loc) => {
      const idx = m[1]!;
      switch (loc) {
        case "en-US":
          return `Fish Speech S2 Pro did not return audio segment ${idx}`;
        case "zh-TW":
          return `Fish Speech S2 Pro 未回傳第 ${idx} 段音訊`;
        case "zh-HK":
          return `Fish Speech S2 Pro 未返回第 ${idx} 段音訊`;
        case "ja-JP":
          return `Fish Speech S2 Pro から第 ${idx} セグメント音声が返されませんでした`;
        case "ko-KR":
          return `Fish Speech S2 Pro가 ${idx}번째 오디오 구간을 반환하지 않았습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^在 (.+?) 中未找到 IndexTTS2 配置文件 config\.yaml$/,
    format: (m, loc) => {
      const dir = m[1]!;
      switch (loc) {
        case "en-US":
          return `IndexTTS2 config.yaml was not found in ${dir}`;
        case "zh-TW":
          return `在 ${dir} 中找不到 IndexTTS2 設定檔 config.yaml`;
        case "zh-HK":
          return `在 ${dir} 中未找到 IndexTTS2 配置檔 config.yaml`;
        case "ja-JP":
          return `${dir} に IndexTTS2 設定ファイル config.yaml が見つかりません`;
        case "ko-KR":
          return `${dir}에서 IndexTTS2 설정 파일 config.yaml을 찾을 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^IndexTTS2 未生成第 (\d+) 段音频：(.+)$/,
    format: (m, loc) => {
      const idx = m[1]!;
      const path = m[2]!;
      switch (loc) {
        case "en-US":
          return `IndexTTS2 did not produce audio segment ${idx}: ${path}`;
        case "zh-TW":
          return `IndexTTS2 未產生第 ${idx} 段音訊：${path}`;
        case "zh-HK":
          return `IndexTTS2 未生成第 ${idx} 段音訊：${path}`;
        case "ja-JP":
          return `IndexTTS2 が第 ${idx} セグメント音声を生成しませんでした: ${path}`;
        case "ko-KR":
          return `IndexTTS2가 ${idx}번째 오디오 구간을 생성하지 못했습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },

  // === server/digital_human.py ===
  {
    re: /^不支持的 (PCM|WAV) 采样位深：(.+?) 字节$/,
    format: (m, loc) => {
      const [, fmt, width] = m;
      switch (loc) {
        case "en-US":
          return `Unsupported ${fmt} sample bit depth: ${width} byte(s)`;
        case "zh-TW":
          return `不支援的 ${fmt} 取樣位元深度：${width} 位元組`;
        case "zh-HK":
          return `不支援的 ${fmt} 採樣位深：${width} 位元組`;
        case "ja-JP":
          return `サポートされていない ${fmt} サンプルビット深度: ${width} バイト`;
        case "ko-KR":
          return `지원하지 않는 ${fmt} 샘플 비트 깊이: ${width}바이트`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^音视频处理失败（退出码 (-?\d+)）(?:：(.*))?$/s,
    format: (m, loc) => {
      const code = m[1]!;
      const tailRaw = (m[2] ?? "").trim();
      const tail = tailRaw ? translateInner(tailRaw, loc) : "";
      switch (loc) {
        case "en-US":
          return `Audio/video processing failed (exit code ${code})${tail ? `: ${tail}` : ""}`;
        case "zh-TW":
          return `影音處理失敗（結束代碼 ${code}）${tail ? `：${tail}` : ""}`;
        case "zh-HK":
          return `音影片處理失敗（退出碼 ${code}）${tail ? `：${tail}` : ""}`;
        case "ja-JP":
          return `音声・動画処理に失敗しました（終了コード ${code}）${tail ? `: ${tail}` : ""}`;
        case "ko-KR":
          return `오디오/비디오 처리 실패 (종료 코드 ${code})${tail ? `: ${tail}` : ""}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^音视频处理命令失败（退出码 (-?\d+)）$/,
    format: (m, loc) => {
      const code = m[1]!;
      switch (loc) {
        case "en-US":
          return `Audio/video processing command failed (exit code ${code})`;
        case "zh-TW":
          return `影音處理命令失敗（結束代碼 ${code}）`;
        case "zh-HK":
          return `音影片處理命令失敗（退出碼 ${code}）`;
        case "ja-JP":
          return `音声・動画処理コマンドが失敗しました（終了コード ${code}）`;
        case "ko-KR":
          return `오디오/비디오 처리 명령 실패 (종료 코드 ${code})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^无法从远端 ComfyUI 获取分段输出文件：(.+)$/,
    format: (m, loc) => {
      const fname = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unable to fetch segment output file from remote ComfyUI: ${fname}`;
        case "zh-TW":
          return `無法從遠端 ComfyUI 取得分段輸出檔案：${fname}`;
        case "zh-HK":
          return `無法從遠端 ComfyUI 獲取分段輸出檔案：${fname}`;
        case "ja-JP":
          return `リモート ComfyUI からセグメント出力ファイルを取得できません: ${fname}`;
        case "ko-KR":
          return `원격 ComfyUI에서 구간 출력 파일을 가져올 수 없습니다: ${fname}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^无法从第 (\d+) 段视频提取尾帧用于长视频衔接(?:：(.+))?$/s,
    format: (m, loc) => {
      const idx = m[1]!;
      const detail = m[2] ? translateInner(m[2], loc) : "";
      switch (loc) {
        case "en-US":
          return detail
            ? `Unable to extract tail frame from video segment ${idx} for long-video continuation: ${detail}`
            : `Unable to extract tail frame from video segment ${idx} for long-video continuation`;
        case "zh-TW":
          return detail
            ? `無法從第 ${idx} 段影片擷取末影格以進行長影片銜接：${detail}`
            : `無法從第 ${idx} 段影片擷取末影格以進行長影片銜接`;
        case "zh-HK":
          return detail
            ? `無法從第 ${idx} 段影片提取尾幀用於長影片銜接：${detail}`
            : `無法從第 ${idx} 段影片提取尾幀用於長影片銜接`;
        case "ja-JP":
          return detail
            ? `長尺動画の連結用に第 ${idx} セグメント動画から末尾フレームを抽出できませんでした: ${detail}`
            : `長尺動画の連結用に第 ${idx} セグメント動画から末尾フレームを抽出できませんでした`;
        case "ko-KR":
          return detail
            ? `장편 영상 연결을 위해 ${idx}번째 영상 구간에서 마지막 프레임을 추출할 수 없습니다: ${detail}`
            : `장편 영상 연결을 위해 ${idx}번째 영상 구간에서 마지막 프레임을 추출할 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前模板不支持所选语音引擎：(.+)$/,
    format: (m, loc) => {
      const engine = m[1]!;
      switch (loc) {
        case "en-US":
          return `Selected TTS engine is not supported by this template: ${engine}`;
        case "zh-TW":
          return `目前範本不支援所選語音引擎：${engine}`;
        case "zh-HK":
          return `目前範本不支援所選語音引擎：${engine}`;
        case "ja-JP":
          return `このテンプレートは選択された音声エンジンに対応していません: ${engine}`;
        case "ko-KR":
          return `현재 템플릿은 선택한 음성 엔진을 지원하지 않습니다: ${engine}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^释放 ComfyUI 显存时出现非致命提示：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Non-fatal warning while releasing ComfyUI VRAM: ${detail}`;
        case "zh-TW":
          return `釋放 ComfyUI VRAM 時出現非致命提示：${detail}`;
        case "zh-HK":
          return `釋放 ComfyUI 顯存時出現非致命提示：${detail}`;
        case "ja-JP":
          return `ComfyUI の VRAM 解放時に軽微な警告が発生しました: ${detail}`;
        case "ko-KR":
          return `ComfyUI VRAM 해제 중 치명적이지 않은 경고 발생: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在使用远端 TTS 引擎（(.+?)）合成高保真配音$/,
    format: (m, loc) => {
      const engine = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Synthesizing high-fidelity voiceover with remote TTS engine (${engine})`;
        case "zh-TW":
          return `正在使用遠端 TTS 引擎（${engine}）合成高傳真配音`;
        case "zh-HK":
          return `正在使用遠端 TTS 引擎（${engine}）合成高保真配音`;
        case "ja-JP":
          return `リモート TTS エンジン（${engine}）で高音質音声を合成中`;
        case "ko-KR":
          return `원격 TTS 엔진(${engine})으로 고음질 더빙을 합성하는 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端配音完成，音频总时长 ([\d.]+) 秒$/,
    format: (m, loc) => {
      const dur = m[1]!;
      switch (loc) {
        case "en-US":
          return `Remote voiceover completed; total audio duration ${dur}s`;
        case "zh-TW":
          return `遠端配音完成，音訊總時長 ${dur} 秒`;
        case "zh-HK":
          return `遠端配音完成，音訊總時長 ${dur} 秒`;
        case "ja-JP":
          return `リモート音声合成が完了しました（合計 ${dur} 秒）`;
        case "ko-KR":
          return `원격 더빙 완료, 전체 오디오 길이 ${dur}초`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^音频预处理完成，总时长 ([\d.]+) 秒，检测到 (\d+) 处自然换气静音点$/,
    format: (m, loc) => {
      const [, dur, count] = m;
      switch (loc) {
        case "en-US":
          return `Audio preprocessing completed (${dur}s total; detected ${count} natural silence valley(s))`;
        case "zh-TW":
          return `音訊前處理完成，總時長 ${dur} 秒，偵測到 ${count} 處自然換氣靜音點`;
        case "zh-HK":
          return `音訊預處理完成，總時長 ${dur} 秒，檢測到 ${count} 處自然換氣靜音點`;
        case "ja-JP":
          return `音声前処理が完了しました（合計 ${dur} 秒、自然な無音ポイントを ${count} 箇所検出）`;
        case "ko-KR":
          return `오디오 전처리 완료 (총 ${dur}초, 자연 무음 구간 ${count}곳 감지)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^音频预处理完成，总时长 ([\d.]+) 秒$/,
    format: (m, loc) => {
      const dur = m[1]!;
      switch (loc) {
        case "en-US":
          return `Audio preprocessing completed (${dur}s total)`;
        case "zh-TW":
          return `音訊前處理完成，總時長 ${dur} 秒`;
        case "zh-HK":
          return `音訊預處理完成，總時長 ${dur} 秒`;
        case "ja-JP":
          return `音声前処理が完了しました（合計 ${dur} 秒）`;
        case "ko-KR":
          return `오디오 전처리 완료 (총 ${dur}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的音频来源模式：(.+)$/,
    format: (m, loc) => {
      const mode = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unsupported audio source mode: ${mode}`;
        case "zh-TW":
          return `不支援的音訊來源模式：${mode}`;
        case "zh-HK":
          return `不支援的音訊來源模式：${mode}`;
        case "ja-JP":
          return `サポートされていない音声ソースモード: ${mode}`;
        case "ko-KR":
          return `지원하지 않는 오디오 소스 모드: ${mode}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在预处理主播参考图（(\d+)[×x](\d+)）$/,
    format: (m, loc) => {
      const [, w, h] = m;
      switch (loc) {
        case "en-US":
          return `Preprocessing presenter portrait (${w}x${h})`;
        case "zh-TW":
          return `正在前處理主播參考圖（${w}×${h}）`;
        case "zh-HK":
          return `正在預處理主播參考圖（${w}×${h}）`;
        case "ja-JP":
          return `プレゼンター参照画像を前処理中（${w}×${h}）`;
        case "ko-KR":
          return `진행자 참조 이미지 전처리 중 (${w}x${h})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在准备远端视频环境与模型权重：(.+)$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Preparing remote video environment and model weights: ${wfName}`;
        case "zh-TW":
          return `正在準備遠端影片環境與模型權重：${wfName}`;
        case "zh-HK":
          return `正在準備遠端影片環境與模型權重：${wfName}`;
        case "ja-JP":
          return `リモート動画環境とモデル重みを準備中: ${wfName}`;
        case "ko-KR":
          return `원격 영상 환경 및 모델 가중치 준비 중: ${wfName}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^分段规划完成：共 (\d+) 段（总时长 ([\d.]+) 秒，基础渲染 (\d+)[×x](\d+)，目标输出 (\d+)[×x](\d+) @ (\d+)fps）$/,
    format: (m, loc) => {
      const [, segs, dur, bw, bh, tw, th, fps] = m;
      switch (loc) {
        case "en-US":
          return `Segment plan ready: ${segs} segment(s) (${dur}s total, base ${bw}x${bh}, target ${tw}x${th} @ ${fps} fps)`;
        case "zh-TW":
          return `分段規劃完成：共 ${segs} 段（總時長 ${dur} 秒，基礎算繪 ${bw}×${bh}，目標輸出 ${tw}×${th} @ ${fps}fps）`;
        case "zh-HK":
          return `分段規劃完成：共 ${segs} 段（總時長 ${dur} 秒，基礎渲染 ${bw}×${bh}，目標輸出 ${tw}×${th} @ ${fps}fps）`;
        case "ja-JP":
          return `セグメント計画が完了しました: 計 ${segs} セグメント（合計 ${dur} 秒、ベース ${bw}×${bh}、出力 ${tw}×${th} @ ${fps}fps）`;
        case "ko-KR":
          return `구간 계획 완료: 총 ${segs}개 구간 (전체 ${dur}초, 기본 렌더링 ${bw}x${bh}, 목표 출력 ${tw}x${th} @ ${fps}fps)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · 正在渲染 第 (\d+)\/(\d+) 段（([\d.]+)s[–-]([\d.]+)s，(\d+) 帧）$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      const [, , cur, total, start, end, frames] = m;
      switch (loc) {
        case "en-US":
          return `${wfName} · Rendering Segment ${cur}/${total} (${start}s-${end}s, ${frames} frames)`;
        case "zh-TW":
          return `${wfName} · 正在算繪 第 ${cur}/${total} 段（${start}s–${end}s，${frames} 影格）`;
        case "zh-HK":
          return `${wfName} · 正在渲染 第 ${cur}/${total} 段（${start}s–${end}s，${frames} 幀）`;
        case "ja-JP":
          return `${wfName} · セグメント ${cur}/${total} をレンダリング中（${start}s–${end}s、${frames} フレーム）`;
        case "ko-KR":
          return `${wfName} · 세그먼트 ${cur}/${total} 렌더링 중 (${start}s-${end}s, ${frames}프레임)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^第 (\d+)\/(\d+) 段 执行完成但未产出(?:有效)?视频文件$/,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      const valid = m[0].includes("有效");
      switch (loc) {
        case "en-US":
          return valid
            ? `${seg} completed without producing a valid video file`
            : `${seg} completed without producing a video file`;
        case "zh-TW":
          return valid
            ? `${seg} 執行完成但未產出有效影片檔案`
            : `${seg} 執行完成但未產出影片檔案`;
        case "zh-HK":
          return valid
            ? `${seg} 執行完成但未產出有效影片檔案`
            : `${seg} 執行完成但未產出影片檔案`;
        case "ja-JP":
          return valid
            ? `${seg} は完了しましたが有効な動画ファイルが出力されませんでした`
            : `${seg} は完了しましたが動画ファイルが出力されませんでした`;
        case "ko-KR":
          return valid
            ? `${seg} 실행은 완료되었으나 유효한 동영상 파일이 생성되지 않았습니다`
            : `${seg} 실행은 완료되었으나 동영상 파일이 생성되지 않았습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^第 (\d+)\/(\d+) 段 返回了图片而非视频文件，请检查视频输出节点格式$/,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${seg} returned an image instead of a video file; please check the video output node format`;
        case "zh-TW":
          return `${seg} 回傳了圖片而非影片檔案，請檢查影片輸出節點格式`;
        case "zh-HK":
          return `${seg} 返回了圖片而非影片檔案，請檢查影片輸出節點格式`;
        case "ja-JP":
          return `${seg} は動画ファイルではなく画像を返しました。動画出力ノードの形式を確認してください`;
        case "ko-KR":
          return `${seg}에서 동영상이 아닌 이미지 파일이 반환되었습니다. 동영상 출력 노드 형식을 확인해 주세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^第 (\d+)\/(\d+) 段 下载的视频文件为空$/,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `Downloaded video file for ${seg} is empty`;
        case "zh-TW":
          return `${seg} 下載的影片檔案為空`;
        case "zh-HK":
          return `${seg} 下載的影片檔案為空`;
        case "ja-JP":
          return `${seg} でダウンロードした動画ファイルが空です`;
        case "ko-KR":
          return `${seg}에서 다운로드한 동영상 파일이 비어 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · 第 (\d+)\/(\d+) 段 渲染完成（耗时 ([^）]+)）$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      const seg = formatSegmentLabel(m[2]!, m[3]!, loc);
      const dur = translateZhDuration(m[4]!, loc);
      switch (loc) {
        case "en-US":
          return `${wfName} · ${seg} rendered (took ${dur})`;
        case "zh-TW":
          return `${wfName} · ${seg} 算圖完成（耗時 ${dur}）`;
        case "zh-HK":
          return `${wfName} · ${seg} 渲染完成（耗時 ${dur}）`;
        case "ja-JP":
          return `${wfName} · ${seg} レンダリング完了（所要 ${dur}）`;
        case "ko-KR":
          return `${wfName} · ${seg} 렌더링 완료 (소요 ${dur})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · 数字人长视频生成完成（共 (\d+) 段，([\d.]+) 秒，(\d+)[×x](\d+)(?:，总耗时 ([^）]+))?）$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      const [, , segs, dur, w, h, rawTotal] = m;
      const totalDur = rawTotal ? translateZhDuration(rawTotal, loc) : "";
      switch (loc) {
        case "en-US":
          return totalDur
            ? `${wfName} · Digital human video completed (${segs} segment(s), ${dur}s, ${w}x${h}, total ${totalDur})`
            : `${wfName} · Digital human video completed (${segs} segment(s), ${dur}s, ${w}x${h})`;
        case "zh-TW":
          return totalDur
            ? `${wfName} · 數位人長影片產生完成（共 ${segs} 段，${dur} 秒，${w}×${h}，總耗時 ${totalDur}）`
            : `${wfName} · 數位人長影片產生完成（共 ${segs} 段，${dur} 秒，${w}×${h}）`;
        case "zh-HK":
          return totalDur
            ? `${wfName} · 數碼人長影片生成完成（共 ${segs} 段，${dur} 秒，${w}×${h}，總耗時 ${totalDur}）`
            : `${wfName} · 數碼人長影片生成完成（共 ${segs} 段，${dur} 秒，${w}×${h}）`;
        case "ja-JP":
          return totalDur
            ? `${wfName} · デジタルヒューマン長尺動画の生成が完了しました（計 ${segs} セグメント、${dur} 秒、${w}×${h}、総所要 ${totalDur}）`
            : `${wfName} · デジタルヒューマン長尺動画の生成が完了しました（計 ${segs} セグメント、${dur} 秒、${w}×${h}）`;
        case "ko-KR":
          return totalDur
            ? `${wfName} · 디지털 휴먼 장편 영상 생성 완료 (총 ${segs}개 구간, ${dur}초, ${w}x${h}, 총 소요 ${totalDur})`
            : `${wfName} · 디지털 휴먼 장편 영상 생성 완료 (총 ${segs}개 구간, ${dur}초, ${w}x${h})`;
        default:
          return m[0];
      }
    },
  },

  // === LongCat 1.6 Native Flagship Stage 1-4 worker progress rules ===
  {
    re: /^正在准备远端原生 LongCat 1\.6 环境与权重（(.+?)）$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Preparing remote native LongCat 1.6 environment and weights (${detail})`;
        case "zh-TW":
          return `正在準備遠端原生 LongCat 1.6 環境與權重（${detail}）`;
        case "zh-HK":
          return `正在準備遠端原生 LongCat 1.6 環境與權重（${detail}）`;
        case "ja-JP":
          return `リモートネイティブ LongCat 1.6 環境とモデル重みを準備中（${detail}）`;
        case "ko-KR":
          return `원격 네이티브 LongCat 1.6 환경 및 가중치 준비 중 (${detail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在执行原生 720p LongCat 1\.6 渲染（(.+?)）$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Rendering native 720p LongCat 1.6 video (${detail})`;
        case "zh-TW":
          return `正在執行原生 720p LongCat 1.6 算繪（${detail}）`;
        case "zh-HK":
          return `正在執行原生 720p LongCat 1.6 渲染（${detail}）`;
        case "ja-JP":
          return `ネイティブ 720p LongCat 1.6 動画をレンダリング中（${detail}）`;
        case "ko-KR":
          return `네이티브 720p LongCat 1.6 영상 렌더링 중 (${detail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在执行全画幅时空扩散超分（(.+?)）$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Running full-frame spatiotemporal diffusion upscaling (${detail})`;
        case "zh-TW":
          return `正在執行全畫幅時空擴散超解析（${detail}）`;
        case "zh-HK":
          return `正在執行全畫幅時空擴散超分（${detail}）`;
        case "ja-JP":
          return `全画面時空間拡散超解像を実行中（${detail}）`;
        case "ko-KR":
          return `풀프레임 시공간 확산 업스케일 실행 중 (${detail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在准备远端全画幅超分环境与权重（(.+?)）$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Preparing remote full-frame super-resolution environment and weights (${detail})`;
        case "zh-TW":
          return `正在準備遠端全畫幅超解析環境與權重（${detail}）`;
        case "zh-HK":
          return `正在準備遠端全畫幅超分環境與權重（${detail}）`;
        case "ja-JP":
          return `リモート全画面超解像環境とモデル重みを準備中（${detail}）`;
        case "ko-KR":
          return `원격 풀프레임 초해상도 환경 및 가중치 준비 중 (${detail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在准备 (.+?) 扩散超分环境$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Preparing ${label} diffusion super-resolution environment`;
        case "zh-TW":
          return `正在準備 ${label} 擴散超解析環境`;
        case "zh-HK":
          return `正在準備 ${label} 擴散超分環境`;
        case "ja-JP":
          return `${label} 拡散超解像環境を準備中`;
        case "ko-KR":
          return `${label} 확산 초해상도 환경 준비 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 扩散超分环境与权重已就绪$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} diffusion super-resolution environment and weights ready`;
        case "zh-TW":
          return `${label} 擴散超解析環境與權重已就緒`;
        case "zh-HK":
          return `${label} 擴散超分環境與權重已就緒`;
        case "ja-JP":
          return `${label} 拡散超解像環境とモデル重みの準備が完了しました`;
        case "ko-KR":
          return `${label} 확산 초해상도 환경 및 가중치 준비 완료`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用已检出的远端源码：(.+?)@([0-9a-f]+)$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      const rev = m[2]!;
      switch (loc) {
        case "en-US":
          return `Reusing checked-out remote source: ${label}@${rev}`;
        case "zh-TW":
          return `重複使用已檢出的遠端原始碼：${label}@${rev}`;
        case "zh-HK":
          return `複用已檢出的遠端源碼：${label}@${rev}`;
        case "ja-JP":
          return `チェックアウト済みのリモートソースを再利用中: ${label}@${rev}`;
        case "ko-KR":
          return `체크아웃된 원격 소스 재사용 중: ${label}@${rev}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在检出\s*(.+?)\s*固定版本源码：([0-9a-f]+)$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      const rev = m[2]!;
      switch (loc) {
        case "en-US":
          return `Checking out pinned ${label} source: ${rev}`;
        case "zh-TW":
          return `正在檢出 ${label} 固定版本原始碼：${rev}`;
        case "zh-HK":
          return `正在檢出 ${label} 固定版本源碼：${rev}`;
        case "ja-JP":
          return `${label} の固定バージョンソースをチェックアウト中: ${rev}`;
        case "ko-KR":
          return `${label} 고정 버전 소스 체크아웃 중: ${rev}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用已就绪的隔离环境：(.+)$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Reusing ready isolated environment: ${label}`;
        case "zh-TW":
          return `重複使用已就緒的隔離環境：${label}`;
        case "zh-HK":
          return `複用已就緒的隔離環境：${label}`;
        case "ja-JP":
          return `準備済みの隔離環境を再利用中: ${label}`;
        case "ko-KR":
          return `준비된 격리 환경 재사용 중: ${label}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在创建隔离虚拟环境：(.+?)（保留宿主 PyTorch）$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Creating isolated virtual environment: ${label} (preserving host PyTorch)`;
        case "zh-TW":
          return `正在建立隔離虛擬環境：${label}（保留宿主 PyTorch）`;
        case "zh-HK":
          return `正在建立隔離虛擬環境：${label}（保留宿主 PyTorch）`;
        case "ja-JP":
          return `隔離仮想環境を作成中: ${label}（ホストの PyTorch を保持）`;
        case "ko-KR":
          return `격리 가상 환경 생성 중: ${label} (호스트 PyTorch 유지)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在安装\s*(.+?)\s*运行依赖（共 (\d+) 个包，保留现有 PyTorch）$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      const count = m[2]!;
      switch (loc) {
        case "en-US":
          return `Installing ${label} runtime dependencies (${count} packages, preserving existing PyTorch)`;
        case "zh-TW":
          return `正在安裝 ${label} 執行相依套件（共 ${count} 個套件，保留現有 PyTorch）`;
        case "zh-HK":
          return `正在安裝 ${label} 運行依賴（共 ${count} 個套件，保留現有 PyTorch）`;
        case "ja-JP":
          return `${label} の実行依存パッケージをインストール中（計 ${count} 個、既存 PyTorch を保持）`;
        case "ko-KR":
          return `${label} 실행 종속성 설치 중 (총 ${count}개 패키지, 기존 PyTorch 유지)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*隔离环境已就绪$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} isolated environment ready`;
        case "zh-TW":
          return `${label} 隔離環境已就緒`;
        case "zh-HK":
          return `${label} 隔離環境已就緒`;
        case "ja-JP":
          return `${label} 隔離環境の準備が完了しました`;
        case "ko-KR":
          return `${label} 격리 환경 준비 완료`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用已缓存的模型权重：(.+?)（(.+?)）$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      const repo = m[2]!;
      switch (loc) {
        case "en-US":
          return `Reusing cached model weights: ${label} (${repo})`;
        case "zh-TW":
          return `重複使用已快取的模型權重：${label}（${repo}）`;
        case "zh-HK":
          return `複用已快取的模型權重：${label}（${repo}）`;
        case "ja-JP":
          return `キャッシュ済みモデル重みを再利用中: ${label}（${repo}）`;
        case "ko-KR":
          return `캐시된 모델 가중치 재사용 중: ${label} (${repo})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在下载模型权重：(.+?)（(.+?)@([0-9a-f]+)）$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      const repo = m[2]!;
      const rev = m[3]!;
      switch (loc) {
        case "en-US":
          return `Downloading model weights: ${label} (${repo}@${rev})`;
        case "zh-TW":
          return `正在下載模型權重：${label}（${repo}@${rev}）`;
        case "zh-HK":
          return `正在下載模型權重：${label}（${repo}@${rev}）`;
        case "ja-JP":
          return `モデル重みをダウンロード中: ${label}（${repo}@${rev}）`;
        case "ko-KR":
          return `모델 가중치 다운로드 중: ${label} (${repo}@${rev})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*模型权重准备完成$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} model weights ready`;
        case "zh-TW":
          return `${label} 模型權重準備完成`;
        case "zh-HK":
          return `${label} 模型權重準備完成`;
        case "ja-JP":
          return `${label} モデル重みの準備が完了しました`;
        case "ko-KR":
          return `${label} 모델 가중치 준비 완료`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^未检测到可用远端 GPU，LongCat-Video-Avatar 1\.6 原生管线至少需要 ([\d.]+) GiB（(\d+) MiB）显存$/,
    format: (m, loc) => {
      const [, gib, mib] = m;
      switch (loc) {
        case "en-US":
          return `No usable remote GPU detected; LongCat-Video-Avatar 1.6 native pipeline requires at least ${gib} GiB (${mib} MiB) VRAM`;
        case "zh-TW":
          return `未偵測到可用遠端 GPU，LongCat-Video-Avatar 1.6 原生管線至少需要 ${gib} GiB（${mib} MiB）VRAM`;
        case "zh-HK":
          return `未檢測到可用遠端 GPU，LongCat-Video-Avatar 1.6 原生管線至少需要 ${gib} GiB（${mib} MiB）顯存`;
        case "ja-JP":
          return `利用可能なリモート GPU が検出されません。LongCat-Video-Avatar 1.6 ネイティブパイプラインには最低 ${gib} GiB（${mib} MiB）の VRAM が必要です`;
        case "ko-KR":
          return `사용 가능한 원격 GPU가 감지되지 않았습니다. LongCat-Video-Avatar 1.6 네이티브 파이프라인에는 최소 ${gib} GiB (${mib} MiB) VRAM이 필요합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 GPU 显存不足：当前 (.+?) 仅 ([\d.]+) GiB（(\d+) MiB），LongCat-Video-Avatar 1\.6 原生管线至少需要 ([\d.]+) GiB（(\d+) MiB）显存$/,
    format: (m, loc) => {
      const [, gpu, curGib, curMib, reqGib, reqMib] = m;
      const gpuName = translateInner(gpu!, loc);
      switch (loc) {
        case "en-US":
          return `Insufficient remote GPU VRAM: current ${gpuName} has only ${curGib} GiB (${curMib} MiB); LongCat-Video-Avatar 1.6 native pipeline requires at least ${reqGib} GiB (${reqMib} MiB)`;
        case "zh-TW":
          return `遠端 GPU VRAM 不足：目前 ${gpuName} 僅 ${curGib} GiB（${curMib} MiB），LongCat-Video-Avatar 1.6 原生管線至少需要 ${reqGib} GiB（${reqMib} MiB）VRAM`;
        case "zh-HK":
          return `遠端 GPU 顯存不足：目前 ${gpuName} 僅 ${curGib} GiB（${curMib} MiB），LongCat-Video-Avatar 1.6 原生管線至少需要 ${reqGib} GiB（${reqMib} MiB）顯存`;
        case "ja-JP":
          return `リモート GPU の VRAM 不足: 現在の ${gpuName} は ${curGib} GiB（${curMib} MiB）のみで、LongCat-Video-Avatar 1.6 ネイティブパイプラインには最低 ${reqGib} GiB（${reqMib} MiB）必要です`;
        case "ko-KR":
          return `원격 GPU VRAM 부족: 현재 ${gpuName}에는 ${curGib} GiB (${curMib} MiB)만 있으며, LongCat-Video-Avatar 1.6 네이티브 파이프라인은 최소 ${reqGib} GiB (${reqMib} MiB) VRAM이 필요합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端工作区磁盘空间不足：权重已缓存(或将写入 Drive)?，但生成与临时转码仍需至少 ([\d.]+) GiB（(\d+) MiB）可用空间，当前仅剩 ([\d.]+) GiB（(\d+) MiB）$/,
    format: (m, loc) => {
      const hasDrive = Boolean(m[1]);
      const reqGib = m[2]!;
      const reqMib = m[3]!;
      const freeGib = m[4]!;
      const freeMib = m[5]!;
      switch (loc) {
        case "en-US":
          return `Insufficient remote workspace disk space: weights are cached${hasDrive ? " or will be written to Drive" : ""}, but rendering and temp transcoding still require at least ${reqGib} GiB (${reqMib} MiB) free (${freeGib} GiB / ${freeMib} MiB remaining)`;
        case "zh-TW":
          return `遠端工作區磁碟空間不足：權重已快取${hasDrive ? "或將寫入 Drive" : ""}，但產生與暫存轉碼仍需至少 ${reqGib} GiB（${reqMib} MiB）可用空間，目前僅剩 ${freeGib} GiB（${freeMib} MiB）`;
        case "zh-HK":
          return `遠端工作區磁碟空間不足：權重已快取${hasDrive ? "或將寫入 Drive" : ""}，但生成與臨時轉碼仍需至少 ${reqGib} GiB（${reqMib} MiB）可用空間，目前僅剩 ${freeGib} GiB（${freeMib} MiB）`;
        case "ja-JP":
          return `リモート作業領域のディスク空き容量が不足しています: 重みはキャッシュ済み${hasDrive ? "または Drive へ保存されます" : ""}が、生成と一時変換に最低 ${reqGib} GiB（${reqMib} MiB）必要です（現在の空き ${freeGib} GiB / ${freeMib} MiB）`;
        case "ko-KR":
          return `원격 워크스페이스 디스크 공간 부족: 가중치는 캐시되어 있${hasDrive ? "거나 Drive에 기록되" : ""}지만 생성 및 임시 변환에 최소 ${reqGib} GiB (${reqMib} MiB) 여유 공간이 필요합니다 (현재 ${freeGib} GiB / ${freeMib} MiB 남음)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端磁盘空间不足：LongCat-Video-Avatar 1\.6 原生管线与超分模型至少需要 ([\d.]+) GiB（(\d+) MiB）可用空间，当前仅剩 ([\d.]+) GiB（(\d+) MiB）$/,
    format: (m, loc) => {
      const [, reqGib, reqMib, freeGib, freeMib] = m;
      switch (loc) {
        case "en-US":
          return `Insufficient remote disk space: LongCat-Video-Avatar 1.6 native pipeline and upscaling models require at least ${reqGib} GiB (${reqMib} MiB) free (${freeGib} GiB / ${freeMib} MiB remaining)`;
        case "zh-TW":
          return `遠端磁碟空間不足：LongCat-Video-Avatar 1.6 原生管線與超解析模型至少需要 ${reqGib} GiB（${reqMib} MiB）可用空間，目前僅剩 ${freeGib} GiB（${freeMib} MiB）`;
        case "zh-HK":
          return `遠端磁碟空間不足：LongCat-Video-Avatar 1.6 原生管線與超分模型至少需要 ${reqGib} GiB（${reqMib} MiB）可用空間，目前僅剩 ${freeGib} GiB（${freeMib} MiB）`;
        case "ja-JP":
          return `リモートディスクの空き容量が不足しています: LongCat-Video-Avatar 1.6 ネイティブパイプラインと超解像モデルには最低 ${reqGib} GiB（${reqMib} MiB）必要です（現在の空き ${freeGib} GiB / ${freeMib} MiB）`;
        case "ko-KR":
          return `원격 디스크 공간 부족: LongCat-Video-Avatar 1.6 네이티브 파이프라인 및 업스케일 모델에는 최소 ${reqGib} GiB (${reqMib} MiB) 여유 공간이 필요합니다 (현재 ${freeGib} GiB / ${freeMib} MiB 남음)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的远端扩散超分引擎：(.+?)（仅支持 seedvr2_7b_sharp、seedvr2_7b、flashvsr_plus(、none、auto)?）$/,
    format: (m, loc) => {
      const engine = m[1]!;
      const hasNoneAuto = Boolean(m[2]);
      switch (loc) {
        case "en-US":
          return `Unsupported remote diffusion super-resolution engine: ${engine} (only seedvr2_7b_sharp, seedvr2_7b${hasNoneAuto ? ", flashvsr_plus, none, and auto" : ", and flashvsr_plus"} are supported)`;
        case "zh-TW":
          return `不支援的遠端擴散超解析引擎：${engine}（僅支援 seedvr2_7b_sharp、seedvr2_7b、flashvsr_plus${hasNoneAuto ? "、none、auto" : ""}）`;
        case "zh-HK":
          return `不支援的遠端擴散超分引擎：${engine}（僅支援 seedvr2_7b_sharp、seedvr2_7b、flashvsr_plus${hasNoneAuto ? "、none、auto" : ""}）`;
        case "ja-JP":
          return `サポートされていないリモート拡散超解像エンジンです: ${engine}（seedvr2_7b_sharp・seedvr2_7b・flashvsr_plus${hasNoneAuto ? "・none・auto" : ""} のみ対応）`;
        case "ko-KR":
          return `지원하지 않는 원격 확산 초해상도 엔진입니다: ${engine} (seedvr2_7b_sharp, seedvr2_7b, flashvsr_plus${hasNoneAuto ? ", none, auto" : ""}만 지원)`;
        default:
          return m[0];
      }
    },
  },

  // === Real _run_longcat_native_pipeline progress & error rules (server/digital_human.py) ===
  {
    re: /^停止远端 ComfyUI 时出现非致命提示：(.+)$/s,
    format: (m, loc) => {
      const exc = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Non-fatal notice while stopping remote ComfyUI: ${exc}`;
        case "zh-TW":
          return `停止遠端 ComfyUI 時出現非致命提示：${exc}`;
        case "zh-HK":
          return `停止遠端 ComfyUI 時出現非致命提示：${exc}`;
        case "ja-JP":
          return `リモート ComfyUI 停止時の非致命的な通知: ${exc}`;
        case "ko-KR":
          return `원격 ComfyUI 중지 중 치명적이지 않은 알림: ${exc}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在准备 LongCat-Video-Avatar 1\.6 原生旗舰环境与权重：(.+)$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Preparing LongCat-Video-Avatar 1.6 native flagship environment and weights: ${wfName}`;
        case "zh-TW":
          return `正在準備 LongCat-Video-Avatar 1.6 原生旗艦環境與權重：${wfName}`;
        case "zh-HK":
          return `正在準備 LongCat-Video-Avatar 1.6 原生旗艦環境與權重：${wfName}`;
        case "ja-JP":
          return `LongCat-Video-Avatar 1.6 ネイティブフラッグシップ環境とモデル重みを準備中: ${wfName}`;
        case "ko-KR":
          return `LongCat-Video-Avatar 1.6 네이티브 플래그십 환경 및 가중치 준비 중: ${wfName}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · 正在使用 LongCat 1\.6 原生引擎渲染 720p 主画面（(\d+)×(\d+)，(\d+) 帧，([^）]+)）$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      const w = m[2]!;
      const h = m[3]!;
      const frames = m[4]!;
      const mode = translateInner(m[5]!, loc);
      switch (loc) {
        case "en-US":
          return `${wfName} · Rendering 720p base video with LongCat 1.6 native engine (${w}x${h}, ${frames} frames, ${mode})`;
        case "zh-TW":
          return `${wfName} · 正在使用 LongCat 1.6 原生引擎算繪 720p 主畫面（${w}×${h}，${frames} 影格，${mode}）`;
        case "zh-HK":
          return `${wfName} · 正在使用 LongCat 1.6 原生引擎渲染 720p 主畫面（${w}×${h}，${frames} 幀，${mode}）`;
        case "ja-JP":
          return `${wfName} · LongCat 1.6 ネイティブエンジンで 720p 本映像をレンダリング中（${w}×${h}、${frames} フレーム、${mode}）`;
        case "ko-KR":
          return `${wfName} · LongCat 1.6 네이티브 엔진으로 720p 기본 영상 렌더링 중 (${w}x${h}, ${frames}프레임, ${mode})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · LongCat 1\.6 原生 720p 主画面生成完成（耗时 ([^）]+)）$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      const dur = translateZhDuration(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${wfName} · LongCat 1.6 native 720p base video completed (${dur})`;
        case "zh-TW":
          return `${wfName} · LongCat 1.6 原生 720p 主畫面產生完成（耗時 ${dur}）`;
        case "zh-HK":
          return `${wfName} · LongCat 1.6 原生 720p 主畫面生成完成（耗時 ${dur}）`;
        case "ja-JP":
          return `${wfName} · LongCat 1.6 ネイティブ 720p 本映像の生成が完了しました（所要 ${dur}）`;
        case "ko-KR":
          return `${wfName} · LongCat 1.6 네이티브 720p 기본 영상 생성 완료 (소요 ${dur})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在准备视频扩散超分引擎与权重：(.+)$/,
    format: (m, loc) => {
      const engine = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Preparing video diffusion super-resolution engine and weights: ${engine}`;
        case "zh-TW":
          return `正在準備影片擴散超解析引擎與權重：${engine}`;
        case "zh-HK":
          return `正在準備影片擴散超分引擎與權重：${engine}`;
        case "ja-JP":
          return `動画拡散超解像エンジンとモデル重みを準備中: ${engine}`;
        case "ko-KR":
          return `영상 확산 초해상도 엔진 및 가중치 준비 중: ${engine}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · 正在执行 ([^）]+?) 视频扩散超分（(\d+)×(\d+)）$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      const engine = translateInner(m[2]!, loc);
      const w = m[3]!;
      const h = m[4]!;
      switch (loc) {
        case "en-US":
          return `${wfName} · Running ${engine} video diffusion upscaling (${w}x${h})`;
        case "zh-TW":
          return `${wfName} · 正在執行 ${engine} 影片擴散超解析（${w}×${h}）`;
        case "zh-HK":
          return `${wfName} · 正在執行 ${engine} 影片擴散超分（${w}×${h}）`;
        case "ja-JP":
          return `${wfName} · ${engine} 動画拡散超解像を実行中（${w}×${h}）`;
        case "ko-KR":
          return `${wfName} · ${engine} 영상 확산 업스케일 실행 중 (${w}x${h})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^视频扩散超分失败（([^）]+)）：未产出有效的无损超分视频$/,
    format: (m, loc) => {
      const engine = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Video diffusion upscaling failed (${engine}): no valid lossless upscaled video produced`;
        case "zh-TW":
          return `影片擴散超解析失敗（${engine}）：未產出有效的無損超解析影片`;
        case "zh-HK":
          return `影片擴散超分失敗（${engine}）：未產出有效的無損超分影片`;
        case "ja-JP":
          return `動画拡散超解像に失敗しました（${engine}）: 有効な可逆超解像動画が生成されませんでした`;
        case "ko-KR":
          return `영상 확산 업스케일 실패 (${engine}): 유효한 무손실 업스케일 영상이 생성되지 않았습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · ([^·]+?) 视频扩散超分完成（耗时 ([^）]+)）$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      const engine = translateInner(m[2]!, loc);
      const dur = translateZhDuration(m[3]!, loc);
      switch (loc) {
        case "en-US":
          return `${wfName} · ${engine} video diffusion upscaling completed (${dur})`;
        case "zh-TW":
          return `${wfName} · ${engine} 影片擴散超解析完成（耗時 ${dur}）`;
        case "zh-HK":
          return `${wfName} · ${engine} 影片擴散超分完成（耗時 ${dur}）`;
        case "ja-JP":
          return `${wfName} · ${engine} 動画拡散超解像が完了しました（所要 ${dur}）`;
        case "ko-KR":
          return `${wfName} · ${engine} 영상 확산 업스케일 완료 (소요 ${dur})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在进行单次母版封装（CRF (\d+) · (\d+)×(\d+) · 48 kHz AAC 256k）$/,
    format: (m, loc) => {
      const [, crf, w, h] = m;
      switch (loc) {
        case "en-US":
          return `Running single-pass master encode (CRF ${crf} · ${w}x${h} · 48 kHz AAC 256k)`;
        case "zh-TW":
          return `正在進行單次母帶封裝（CRF ${crf} · ${w}×${h} · 48 kHz AAC 256k）`;
        case "zh-HK":
          return `正在進行單次母版封裝（CRF ${crf} · ${w}×${h} · 48 kHz AAC 256k）`;
        case "ja-JP":
          return `シングルパスマスターエンコードを実行中（CRF ${crf}・${w}×${h}・48 kHz AAC 256k）`;
        case "ko-KR":
          return `단일 패스 마스터 인코딩 진행 중 (CRF ${crf} · ${w}x${h} · 48 kHz AAC 256k)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) · 原生旗舰数字人生成完成（LongCat 1\.6( \+ [^，]+)?，([\d.]+) 秒，(\d+)×(\d+)，总耗时 ([^）]+)）$/,
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      const vsrNote = m[2] ?? "";
      const sec = m[3]!;
      const w = m[4]!;
      const h = m[5]!;
      const dur = translateZhDuration(m[6]!, loc);
      switch (loc) {
        case "en-US":
          return `${wfName} · Native flagship digital human completed (LongCat 1.6${vsrNote}, ${sec}s, ${w}x${h}, total ${dur})`;
        case "zh-TW":
          return `${wfName} · 原生旗艦數位人產生完成（LongCat 1.6${vsrNote}，${sec} 秒，${w}×${h}，總耗時 ${dur}）`;
        case "zh-HK":
          return `${wfName} · 原生旗艦數碼人生成完成（LongCat 1.6${vsrNote}，${sec} 秒，${w}×${h}，總耗時 ${dur}）`;
        case "ja-JP":
          return `${wfName} · ネイティブフラッグシップ・デジタルヒューマンの生成が完了しました（LongCat 1.6${vsrNote}、${sec} 秒、${w}×${h}、合計 ${dur}）`;
        case "ko-KR":
          return `${wfName} · 네이티브 플래그십 디지털 휴먼 생성 완료 (LongCat 1.6${vsrNote}, ${sec}초, ${w}x${h}, 총 소요 ${dur})`;
        default:
          return m[0];
      }
    },
  },

  // === Standardized worker progress rules (avatar_worker_longcat16.py & vsr_worker_diffusion.py) ===
  {
    re: /^正在初始化 LongCat-Video-Avatar 1\.6（([^，]+)，共 (\d+) 段）$/,
    format: (m, loc) => {
      const mode = translateInner(m[1]!, loc);
      const n = m[2]!;
      switch (loc) {
        case "en-US":
          return `Initializing LongCat-Video-Avatar 1.6 (${mode}, ${n} segments)`;
        case "zh-TW":
          return `正在初始化 LongCat-Video-Avatar 1.6（${mode}，共 ${n} 段）`;
        case "zh-HK":
          return `正在初始化 LongCat-Video-Avatar 1.6（${mode}，共 ${n} 段）`;
        case "ja-JP":
          return `LongCat-Video-Avatar 1.6 を初期化中（${mode}、計 ${n} セグメント）`;
        case "ko-KR":
          return `LongCat-Video-Avatar 1.6 초기화 중 (${mode}, 총 ${n}개 세그먼트)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在准备音频特征（前置人声分离：(开启|关闭)）$/,
    format: (m, loc) => {
      const on = m[1] === "开启";
      switch (loc) {
        case "en-US":
          return `Preparing audio features (vocal separation: ${on ? "on" : "off"})`;
        case "zh-TW":
          return `正在準備音訊特徵（前置人聲分離：${on ? "開啟" : "關閉"}）`;
        case "zh-HK":
          return `正在準備音訊特徵（前置人聲分離：${on ? "開啟" : "關閉"}）`;
        case "ja-JP":
          return `音声特徴を準備中（前処理ボーカル分離: ${on ? "オン" : "オフ"}）`;
        case "ko-KR":
          return `오디오 특징 준비 중 (전처리 보컬 분리: ${on ? "켜짐" : "꺼짐"})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在渲染第 (\d+)\/(\d+) 段（首段 AI2V）$/,
    format: (m, loc) => {
      const [, i, n] = m;
      switch (loc) {
        case "en-US":
          return `Rendering segment ${i}/${n} (initial AI2V)`;
        case "zh-TW":
          return `正在算繪第 ${i}/${n} 段（首段 AI2V）`;
        case "zh-HK":
          return `正在渲染第 ${i}/${n} 段（首段 AI2V）`;
        case "ja-JP":
          return `セグメント ${i}/${n} をレンダリング中（初回 AI2V）`;
        case "ko-KR":
          return `세그먼트 ${i}/${n} 렌더링 중 (초기 AI2V)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在渲染第 (\d+)\/(\d+) 段（13 帧连续续写）$/,
    format: (m, loc) => {
      const [, i, n] = m;
      switch (loc) {
        case "en-US":
          return `Rendering segment ${i}/${n} (13-frame continuous continuation)`;
        case "zh-TW":
          return `正在算繪第 ${i}/${n} 段（13 影格連續續寫）`;
        case "zh-HK":
          return `正在渲染第 ${i}/${n} 段（13 幀連續續寫）`;
        case "ja-JP":
          return `セグメント ${i}/${n} をレンダリング中（13 フレーム連続延長）`;
        case "ko-KR":
          return `세그먼트 ${i}/${n} 렌더링 중 (13프레임 연속 이어쓰기)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已完成第 (\d+)\/(\d+) 段（累计 (\d+)\/(\d+) 帧）$/,
    format: (m, loc) => {
      const [, i, n, written, total] = m;
      switch (loc) {
        case "en-US":
          return `Completed segment ${i}/${n} (${written}/${total} frames written)`;
        case "zh-TW":
          return `已完成第 ${i}/${n} 段（累計 ${written}/${total} 影格）`;
        case "zh-HK":
          return `已完成第 ${i}/${n} 段（累計 ${written}/${total} 幀）`;
        case "ja-JP":
          return `セグメント ${i}/${n} が完了しました（累計 ${written}/${total} フレーム）`;
        case "ko-KR":
          return `세그먼트 ${i}/${n} 완료 (누적 ${written}/${total}프레임)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^LongCat-Video-Avatar 1\.6 渲染完成（共 (\d+) 帧，耗时 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const [, n, s] = m;
      switch (loc) {
        case "en-US":
          return `LongCat-Video-Avatar 1.6 rendering completed (${n} frames, ${s}s)`;
        case "zh-TW":
          return `LongCat-Video-Avatar 1.6 算繪完成（共 ${n} 影格，耗時 ${s} 秒）`;
        case "zh-HK":
          return `LongCat-Video-Avatar 1.6 渲染完成（共 ${n} 幀，耗時 ${s} 秒）`;
        case "ja-JP":
          return `LongCat-Video-Avatar 1.6 のレンダリングが完了しました（計 ${n} フレーム、所要 ${s} 秒）`;
        case "ko-KR":
          return `LongCat-Video-Avatar 1.6 렌더링 완료 (총 ${n}프레임, 소요 ${s}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在启动 (.+?) 超分（短边 (\d+)px，批次 (\d+)，共 (\d+) 批）$/,
    format: (m, loc) => {
      const engine = translateInner(m[1]!, loc);
      const px = m[2]!;
      const b = m[3]!;
      const c = m[4]!;
      switch (loc) {
        case "en-US":
          return `Starting ${engine} upscaling (short side ${px}px, batch ${b}, ${c} batches)`;
        case "zh-TW":
          return `正在啟動 ${engine} 超解析（短邊 ${px}px，批次 ${b}，共 ${c} 批）`;
        case "zh-HK":
          return `正在啟動 ${engine} 超分（短邊 ${px}px，批次 ${b}，共 ${c} 批）`;
        case "ja-JP":
          return `${engine} 超解像を開始中（短辺 ${px}px、バッチ ${b}、計 ${c} バッチ）`;
        case "ko-KR":
          return `${engine} 업스케일 시작 중 (단변 ${px}px, 배치 ${b}, 총 ${c}배치)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在执行 SeedVR2 时空扩散超分（([^）]+)）$/,
    format: (m, loc) => {
      const engine = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Running SeedVR2 spatiotemporal diffusion upscaling (${engine})`;
        case "zh-TW":
          return `正在執行 SeedVR2 時空擴散超解析（${engine}）`;
        case "zh-HK":
          return `正在執行 SeedVR2 時空擴散超分（${engine}）`;
        case "ja-JP":
          return `SeedVR2 時空間拡散超解像を実行中（${engine}）`;
        case "ko-KR":
          return `SeedVR2 시공간 확산 업스케일 실행 중 (${engine})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在加载 FlashVSR\+ v1\.1 流式扩散模型（(\d+)×(\d+)）$/,
    format: (m, loc) => {
      const [, w, h] = m;
      switch (loc) {
        case "en-US":
          return `Loading FlashVSR+ v1.1 streaming diffusion model (${w}x${h})`;
        case "zh-TW":
          return `正在載入 FlashVSR+ v1.1 串流擴散模型（${w}×${h}）`;
        case "zh-HK":
          return `正在載入 FlashVSR+ v1.1 串流擴散模型（${w}×${h}）`;
        case "ja-JP":
          return `FlashVSR+ v1.1 ストリーミング拡散モデルを読み込み中（${w}×${h}）`;
        case "ko-KR":
          return `FlashVSR+ v1.1 스트리밍 확산 모델 로드 중 (${w}x${h})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在执行 FlashVSR\+ v1\.1 流式扩散超分（第 (\d+)\/(\d+) 段）$/,
    format: (m, loc) => {
      const [, i, n] = m;
      switch (loc) {
        case "en-US":
          return `Running FlashVSR+ v1.1 streaming diffusion upscaling (segment ${i}/${n})`;
        case "zh-TW":
          return `正在執行 FlashVSR+ v1.1 串流擴散超解析（第 ${i}/${n} 段）`;
        case "zh-HK":
          return `正在執行 FlashVSR+ v1.1 串流擴散超分（第 ${i}/${n} 段）`;
        case "ja-JP":
          return `FlashVSR+ v1.1 ストリーミング拡散超解像を実行中（セグメント ${i}/${n}）`;
        case "ko-KR":
          return `FlashVSR+ v1.1 스트리밍 확산 업스케일 실행 중 (세그먼트 ${i}/${n})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已完成 (.+?) 视频扩散超分（共 (\d+) 帧，耗时 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const engine = translateInner(m[1]!, loc);
      const n = m[2]!;
      const s = m[3]!;
      switch (loc) {
        case "en-US":
          return `Completed ${engine} video diffusion upscaling (${n} frames, ${s}s)`;
        case "zh-TW":
          return `已完成 ${engine} 影片擴散超解析（共 ${n} 影格，耗時 ${s} 秒）`;
        case "zh-HK":
          return `已完成 ${engine} 影片擴散超分（共 ${n} 幀，耗時 ${s} 秒）`;
        case "ja-JP":
          return `${engine} 動画拡散超解像が完了しました（計 ${n} フレーム、所要 ${s} 秒）`;
        case "ko-KR":
          return `${engine} 영상 확산 업스케일 완료 (총 ${n}프레임, 소요 ${s}초)`;
        default:
          return m[0];
      }
    },
  },

  // === server/avatar_engines.py error & diagnostic rules ===
  {
    re: /^无法检测远端磁盘可用空间（(.+?)），LongCat-Video-Avatar 1\.6 至少需要 ([\d.]+) GiB 可用磁盘空间$/s,
    format: (m, loc) => {
      const exc = translateInner(m[1]!, loc);
      const gib = m[2]!;
      switch (loc) {
        case "en-US":
          return `Unable to check remote free disk space (${exc}); LongCat-Video-Avatar 1.6 requires at least ${gib} GiB free disk space`;
        case "zh-TW":
          return `無法偵測遠端磁碟可用空間（${exc}），LongCat-Video-Avatar 1.6 至少需要 ${gib} GiB 可用磁碟空間`;
        case "zh-HK":
          return `無法檢測遠端磁碟可用空間（${exc}），LongCat-Video-Avatar 1.6 至少需要 ${gib} GiB 可用磁碟空間`;
        case "ja-JP":
          return `リモートディスクの空き容量を確認できません（${exc}）。LongCat-Video-Avatar 1.6 には最低 ${gib} GiB の空き容量が必要です`;
        case "ko-KR":
          return `원격 디스크 여유 공간을 확인할 수 없습니다 (${exc}). LongCat-Video-Avatar 1.6에는 최소 ${gib} GiB의 디스크 여유 공간이 필요합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*长时间无输出与进度更新（超过 (\d+) 秒），进程疑似卡死，请查看运行日志$/,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const sec = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${stage} produced no output or progress updates for over ${sec}s (process may be stalled); check the runtime log`;
        case "zh-TW":
          return `${stage}${zhSep}長時間無輸出與進度更新（超過 ${sec} 秒），程序疑似卡死，請查看執行日誌`;
        case "zh-HK":
          return `${stage}${zhSep}長時間無輸出與進度更新（超過 ${sec} 秒），進程疑似卡死，請查看運行日誌`;
        case "ja-JP":
          return `${stage} で ${sec} 秒以上出力や進捗更新がありません（プロセスが停止している可能性があります）。実行ログを確認してください`;
        case "ko-KR":
          return `${stage}에서 ${sec}초 이상 출력 및 진행 업데이트가 없어 프로세스가 멈춘 것으로 보입니다. 실행 로그를 확인하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*进程退出超时，请查看运行日志$/,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${stage} process exit timed out; check the runtime log`;
        case "zh-TW":
          return `${stage}${zhSep}程序結束逾時，請查看執行日誌`;
        case "zh-HK":
          return `${stage}${zhSep}進程退出逾時，請查看運行日誌`;
        case "ja-JP":
          return `${stage} プロセスの終了がタイムアウトしました。実行ログを確認してください`;
        case "ko-KR":
          return `${stage} 프로세스 종료 시간이 초과되었습니다. 실행 로그를 확인하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*执行超时，请查看运行日志$/,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${stage} execution timed out; check the runtime log`;
        case "zh-TW":
          return `${stage}${zhSep}執行逾時，請查看執行日誌`;
        case "zh-HK":
          return `${stage}${zhSep}執行逾時，請查看運行日誌`;
        case "ja-JP":
          return `${stage} の実行がタイムアウトしました。実行ログを確認してください`;
        case "ko-KR":
          return `${stage} 실행 시간이 초과되었습니다. 실행 로그를 확인하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*执行失败（退出码 (-?\d+)）：(.*)$/s,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const code = m[2]!;
      const tailRaw = (m[3] ?? "").trim();
      const tail = tailRaw ? translateInner(tailRaw, loc) : "";
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${stage} failed (exit code ${code})${tail ? `: ${tail}` : ""}`;
        case "zh-TW":
          return `${stage}${zhSep}執行失敗（結束代碼 ${code}）${tail ? `：${tail}` : ""}`;
        case "zh-HK":
          return `${stage}${zhSep}執行失敗（退出碼 ${code}）${tail ? `：${tail}` : ""}`;
        case "ja-JP":
          return `${stage} の実行に失敗しました（終了コード ${code}）${tail ? `: ${tail}` : ""}`;
        case "ko-KR":
          return `${stage} 실행 실패 (종료 코드 ${code})${tail ? `: ${tail}` : ""}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*源码提交哈希必须为完整 40 位十六进制 SHA：(.+)$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      const commit = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} source commit hash must be a full 40-character hexadecimal SHA: ${commit}`;
        case "zh-TW":
          return `${label}${zhSep}原始碼提交雜湊必須為完整 40 位十六進位 SHA：${commit}`;
        case "zh-HK":
          return `${label}${zhSep}源碼提交哈希必須為完整 40 位十六進制 SHA：${commit}`;
        case "ja-JP":
          return `${label} のソースコミットハッシュは完全な 40 桁の 16 進 SHA である必要があります: ${commit}`;
        case "ko-KR":
          return `${label} 소스 커밋 해시는 전체 40자리 16진수 SHA여야 합니다: ${commit}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端缺少 Git，无法拉取\s*(.+?)\s*源码$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      switch (loc) {
        case "en-US":
          return `Git is not installed on the remote instance; cannot fetch ${label} source`;
        case "zh-TW":
          return `遠端缺少 Git，無法拉取 ${label} 原始碼`;
        case "zh-HK":
          return `遠端缺少 Git，無法拉取 ${label} 源碼`;
        case "ja-JP":
          return `リモート環境に Git がないため、${label} のソースを取得できません`;
        case "ko-KR":
          return `원격 환경에 Git이 없어 ${label} 소스를 가져올 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*源码检出校验失败，HEAD 与目标提交 ([0-9a-f]+) 不一致或关键文件缺失$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      const commit = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} source checkout verification failed: HEAD does not match target commit ${commit} or required files are missing`;
        case "zh-TW":
          return `${label}${zhSep}原始碼檢出驗證失敗，HEAD 與目標提交 ${commit} 不一致或關鍵檔案遺失`;
        case "zh-HK":
          return `${label}${zhSep}源碼檢出校驗失敗，HEAD 與目標提交 ${commit} 不一致或關鍵檔案缺失`;
        case "ja-JP":
          return `${label} のソースチェックアウト検証に失敗しました。HEAD が対象コミット ${commit} と一致しないか重要ファイルが不足しています`;
        case "ko-KR":
          return `${label} 소스 체크아웃 검증 실패: HEAD가 대상 커밋 ${commit}과 일치하지 않거나 필수 파일이 누락되었습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*现有 Git 仓库异常，正在清空并重新检出：(.*)$/s,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      const err = translateInner(m[2]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `Existing ${label} Git repository is corrupted; clearing and re-checking out: ${err}`;
        case "zh-TW":
          return `${label}${zhSep}現有 Git 存放庫異常，正在清空並重新檢出：${err}`;
        case "zh-HK":
          return `${label}${zhSep}現有 Git 倉庫異常，正在清空並重新檢出：${err}`;
        case "ja-JP":
          return `既存の ${label} Git リポジトリに異常があるため、初期化して再チェックアウトしています: ${err}`;
        case "ko-KR":
          return `기존 ${label} Git 저장소에 이상이 있어 초기화 후 다시 체크아웃하는 중: ${err}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*隔离虚拟环境缺少 Python 可执行文件，拒绝回退到宿主环境：(.+)$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      const dir = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} isolated virtual environment is missing the Python executable; refusing to fall back to the host environment: ${dir}`;
        case "zh-TW":
          return `${label}${zhSep}隔離虛擬環境缺少 Python 執行檔，拒絕退回至宿主環境：${dir}`;
        case "zh-HK":
          return `${label}${zhSep}隔離虛擬環境缺少 Python 可執行檔，拒絕回退至宿主環境：${dir}`;
        case "ja-JP":
          return `${label} の隔離仮想環境に Python 実行ファイルがないため、ホスト環境へのフォールバックを拒否しました: ${dir}`;
        case "ko-KR":
          return `${label} 격리 가상 환경에 Python 실행 파일이 없어 호스트 환경으로 폴백하지 않았습니다: ${dir}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*虚拟环境创建不完整，缺少 Python 可执行文件：(.+)$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      const dir = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} virtual environment creation incomplete; Python executable missing: ${dir}`;
        case "zh-TW":
          return `${label}${zhSep}虛擬環境建立不完整，缺少 Python 執行檔：${dir}`;
        case "zh-HK":
          return `${label}${zhSep}虛擬環境建立不完整，缺少 Python 可執行檔：${dir}`;
        case "ja-JP":
          return `${label} の仮想環境作成が不完全です。Python 実行ファイルが見つかりません: ${dir}`;
        case "ko-KR":
          return `${label} 가상 환경 생성이 완료되지 않아 Python 실행 파일이 누락되었습니다: ${dir}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^可选依赖（(.+?)）安装失败，已跳过，(.+?) 将使用原始音频继续运行：(.*)$/s,
    format: (m, loc) => {
      const pkgs = m[1]!;
      const label = translateInner(m[2]!.trim(), loc);
      const err = translateInner(m[3]!.trim(), loc);
      switch (loc) {
        case "en-US":
          return `Optional dependency (${pkgs}) installation failed and was skipped; ${label} will continue with the original audio: ${err}`;
        case "zh-TW":
          return `選用相依套件（${pkgs}）安裝失敗，已略過，${label} 將使用原始音訊繼續執行：${err}`;
        case "zh-HK":
          return `可選依賴（${pkgs}）安裝失敗，已跳過，${label} 將使用原始音訊繼續運行：${err}`;
        case "ja-JP":
          return `任意依存パッケージ（${pkgs}）のインストールに失敗したためスキップしました。${label} は元の音声のまま続行します: ${err}`;
        case "ko-KR":
          return `선택 종속성(${pkgs}) 설치에 실패해 건너뛰었으며, ${label}은(는) 원본 오디오로 계속 실행됩니다: ${err}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^缺少 huggingface_hub，无法下载(?:\s*(.+?)\s*)?模型权重(?:：(.+))?$/,
    format: (m, loc) => {
      const label = m[1] ? translateInner(m[1].trim(), loc) : "";
      const repo = m[2] ?? "";
      switch (loc) {
        case "en-US":
          return `Missing huggingface_hub; cannot download${label ? ` ${label}` : ""} model weights${repo ? `: ${repo}` : ""}`;
        case "zh-TW":
          return `缺少 huggingface_hub，無法下載${label ? ` ${label} ` : ""}模型權重${repo ? `：${repo}` : ""}`;
        case "zh-HK":
          return `缺少 huggingface_hub，無法下載${label ? ` ${label} ` : ""}模型權重${repo ? `：${repo}` : ""}`;
        case "ja-JP":
          return `huggingface_hub が見つからないため${label ? ` ${label} の` : ""}モデル重みをダウンロードできません${repo ? `: ${repo}` : ""}`;
        case "ko-KR":
          return `huggingface_hub가 없어${label ? ` ${label}` : ""} 모델 가중치를 다운로드할 수 없습니다${repo ? `: ${repo}` : ""}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^下载\s*(.+?)\s*模型权重失败（([^）]+)）：(.*)$/s,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      const repo = m[2]!;
      const err = translateInner(m[3]!.trim(), loc);
      switch (loc) {
        case "en-US":
          return `Failed to download ${label} model weights (${repo}): ${err}`;
        case "zh-TW":
          return `下載 ${label} 模型權重失敗（${repo}）：${err}`;
        case "zh-HK":
          return `下載 ${label} 模型權重失敗（${repo}）：${err}`;
        case "ja-JP":
          return `${label} モデル重みのダウンロードに失敗しました（${repo}）: ${err}`;
        case "ko-KR":
          return `${label} 모델 가중치 다운로드 실패 (${repo}): ${err}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*模型权重下载不完整，缺少必需权重文件：(.+)$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      const dir = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} model weight download incomplete; required weight files missing: ${dir}`;
        case "zh-TW":
          return `${label}${zhSep}模型權重下載不完整，缺少必要權重檔案：${dir}`;
        case "zh-HK":
          return `${label}${zhSep}模型權重下載不完整，缺少必需權重檔案：${dir}`;
        case "ja-JP":
          return `${label} モデル重みのダウンロードが不完全です。必要な重みファイルが見つかりません: ${dir}`;
        case "ko-KR":
          return `${label} 모델 가중치 다운로드가 불완전하여 필수 가중치 파일이 누락되었습니다: ${dir}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*执行完成但未生成有效输出清单：(.+)$/,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const path = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${stage} finished without producing a valid output manifest: ${path}`;
        case "zh-TW":
          return `${stage}${zhSep}執行完成但未產生有效輸出清單：${path}`;
        case "zh-HK":
          return `${stage}${zhSep}執行完成但未生成有效輸出清單：${path}`;
        case "ja-JP":
          return `${stage} の実行は完了しましたが、有効な出力マニフェストが生成されませんでした: ${path}`;
        case "ko-KR":
          return `${stage} 실행은 완료되었으나 유효한 출력 매니페스트가 생성되지 않았습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*输出清单解析失败：(.*)$/s,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const err = translateInner(m[2]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `Failed to parse ${stage} output manifest: ${err}`;
        case "zh-TW":
          return `${stage}${zhSep}輸出清單解析失敗：${err}`;
        case "zh-HK":
          return `${stage}${zhSep}輸出清單解析失敗：${err}`;
        case "ja-JP":
          return `${stage} の出力マニフェスト解析に失敗しました: ${err}`;
        case "ko-KR":
          return `${stage} 출력 매니페스트 파싱 실패: ${err}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*输出清单格式无效或为空$/,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${stage} output manifest is invalid or empty`;
        case "zh-TW":
          return `${stage}${zhSep}輸出清單格式無效或為空`;
        case "zh-HK":
          return `${stage}${zhSep}輸出清單格式無效或為空`;
        case "ja-JP":
          return `${stage} の出力マニフェスト形式が無効か空です`;
        case "ko-KR":
          return `${stage} 출력 매니페스트 형식이 유효하지 않거나 비어 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*输出清单缺少有效的输出视频路径（output_video_path）$/,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${stage} output manifest is missing a valid output video path (output_video_path)`;
        case "zh-TW":
          return `${stage}${zhSep}輸出清單缺少有效的輸出影片路徑（output_video_path）`;
        case "zh-HK":
          return `${stage}${zhSep}輸出清單缺少有效的輸出影片路徑（output_video_path）`;
        case "ja-JP":
          return `${stage} の出力マニフェストに有効な出力動画パス（output_video_path）がありません`;
        case "ko-KR":
          return `${stage} 출력 매니페스트에 유효한 출력 영상 경로(output_video_path)가 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*返回错误：(.*)$/s,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const err = translateInner(m[2]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${stage} returned an error: ${err}`;
        case "zh-TW":
          return `${stage}${zhSep}回傳錯誤：${err}`;
        case "zh-HK":
          return `${stage}${zhSep}返回錯誤：${err}`;
        case "ja-JP":
          return `${stage} がエラーを返しました: ${err}`;
        case "ko-KR":
          return `${stage}에서 오류가 반환되었습니다: ${err}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*声明的输出视频不存在或为空：(.+)$/,
    format: (m, loc) => {
      const stage = translateInner(m[1]!.trim(), loc);
      const path = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(stage) ? " " : "";
      switch (loc) {
        case "en-US":
          return `Output video declared by ${stage} does not exist or is empty: ${path}`;
        case "zh-TW":
          return `${stage}${zhSep}宣告的輸出影片不存在或為空：${path}`;
        case "zh-HK":
          return `${stage}${zhSep}聲明的輸出影片不存在或為空：${path}`;
        case "ja-JP":
          return `${stage} が宣言した出力動画が存在しないか空です: ${path}`;
        case "ko-KR":
          return `${stage}에서 선언한 출력 영상이 존재하지 않거나 비어 있습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*路径不合法：(.+)$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!.trim(), loc);
      const path = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `Invalid ${label} path: ${path}`;
        case "zh-TW":
          return `${label}${zhSep}路徑不合法：${path}`;
        case "zh-HK":
          return `${label}${zhSep}路徑不合法：${path}`;
        case "ja-JP":
          return `${label} のパスが不正です: ${path}`;
        case "ko-KR":
          return `${label} 경로가 올바르지 않습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*路径不能包含符号链接：(.+)$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!.trim(), loc);
      const path = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} path cannot contain symbolic links: ${path}`;
        case "zh-TW":
          return `${label}${zhSep}路徑不可包含符號連結：${path}`;
        case "zh-HK":
          return `${label}${zhSep}路徑不能包含符號連結：${path}`;
        case "ja-JP":
          return `${label} のパスにシンボリックリンクを含めることはできません: ${path}`;
        case "ko-KR":
          return `${label} 경로에는 심볼릭 링크를 포함할 수 없습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*必须位于工作目录内：(.+)$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!.trim(), loc);
      const path = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} must be located inside the working directory: ${path}`;
        case "zh-TW":
          return `${label}${zhSep}必須位於工作目錄內：${path}`;
        case "zh-HK":
          return `${label}${zhSep}必須位於工作目錄內：${path}`;
        case "ja-JP":
          return `${label} は作業ディレクトリ内にある必要があります: ${path}`;
        case "ko-KR":
          return `${label}은(는) 작업 디렉터리 내부에 있어야 합니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*不能指向工作目录本身：(.+)$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!.trim(), loc);
      const path = m[2]!;
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} cannot point to the working directory itself: ${path}`;
        case "zh-TW":
          return `${label}${zhSep}不可指向工作目錄本身：${path}`;
        case "zh-HK":
          return `${label}${zhSep}不能指向工作目錄本身：${path}`;
        case "ja-JP":
          return `${label} に作業ディレクトリ自体は指定できません: ${path}`;
        case "ko-KR":
          return `${label}은(는) 작업 디렉터리 자체를 가리킬 수 없습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*权重下载$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} weights download`;
        case "zh-TW":
          return `${label}${zhSep}權重下載`;
        case "zh-HK":
          return `${label}${zhSep}權重下載`;
        case "ja-JP":
          return `${label} モデル重みダウンロード`;
        case "ko-KR":
          return `${label} 가중치 다운로드`;
        default:
          return m[0];
      }
    },
  },

  // === Path validation & pre-existing digital_human.py segment safety errors ===
  {
    re: /^(.+?)不能包含换行符或空字符$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} cannot contain newlines or null characters`;
        case "zh-TW":
          return `${label}${zhSep}不可包含換行符號或空字元`;
        case "zh-HK":
          return `${label}${zhSep}不能包含換行符或空字元`;
        case "ja-JP":
          return `${label} に改行やヌル文字を含めることはできません`;
        case "ko-KR":
          return `${label}에는 줄바꿈이나 널 문자를 포함할 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^第 (\d+)\/(\d+) 段 视频文件无效或为符号链接$/,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${seg} video file is invalid or a symbolic link`;
        case "zh-TW":
          return `${seg} 影片檔案無效或為符號連結`;
        case "zh-HK":
          return `${seg} 影片檔案無效或為符號連結`;
        case "ja-JP":
          return `${seg} の動画ファイルが無効かシンボリックリンクです`;
        case "ko-KR":
          return `${seg} 영상 파일이 유효하지 않거나 심볼릭 링크입니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^第 (\d+)\/(\d+) 段 返回的分段视频路径无效或为符号链接$/,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${seg} returned an invalid or symbolic-link segment video path`;
        case "zh-TW":
          return `${seg} 回傳的分段影片路徑無效或為符號連結`;
        case "zh-HK":
          return `${seg} 返回的分段影片路徑無效或為符號連結`;
        case "ja-JP":
          return `${seg} が返したセグメント動画パスが無効かシンボリックリンクです`;
        case "ko-KR":
          return `${seg}에서 반환된 세그먼트 영상 경로가 유효하지 않거나 심볼릭 링크입니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^第 (\d+)\/(\d+) 段 分段视频路径安全校验失败$/,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${seg} segment video path failed safety validation`;
        case "zh-TW":
          return `${seg} 分段影片路徑安全驗證失敗`;
        case "zh-HK":
          return `${seg} 分段影片路徑安全校驗失敗`;
        case "ja-JP":
          return `${seg} のセグメント動画パス安全性検証に失敗しました`;
        case "ko-KR":
          return `${seg} 세그먼트 영상 경로 보안 검증에 실패했습니다`;
        default:
          return m[0];
      }
    },
  },

  // === LongCat remote GPU acceleration stack, SageAttention cache & built-in voice reference logs ===
  {
    re: /^已启用 LongCat 远端加速栈：注意力=([^，]+)，量化=([^，]+)，显存常驻=(是|否)，torch\.compile=(开|关)$/,
    format: (m, loc) => {
      const attn = translateLongCatAccelToken(m[1]!, loc);
      const quant = translateLongCatAccelToken(m[2]!, loc);
      const resident = m[3] === "是";
      const compile = m[4] === "开";
      switch (loc) {
        case "en-US":
          return `Enabled LongCat remote acceleration stack: attention=${attn}, quantization=${quant}, resident VRAM=${resident ? "yes" : "no"}, torch.compile=${compile ? "on" : "off"}`;
        case "zh-TW":
          return `已啟用 LongCat 遠端加速堆疊：注意力=${attn}，量化=${quant}，顯示記憶體常駐=${resident ? "是" : "否"}，torch.compile=${compile ? "開" : "關"}`;
        case "zh-HK":
          return `已啟用 LongCat 遠端加速棧：注意力=${attn}，量化=${quant}，顯存常駐=${resident ? "是" : "否"}，torch.compile=${compile ? "開" : "關"}`;
        case "ja-JP":
          return `LongCat リモート高速化スタックを有効化しました: Attention=${attn}、量子化=${quant}、VRAM 常駐=${resident ? "有効" : "無効"}、torch.compile=${compile ? "オン" : "オフ"}`;
        case "ko-KR":
          return `LongCat 원격 가속 스택 활성화됨: 어텐션=${attn}, 양자화=${quant}, VRAM 상주=${resident ? "예" : "아니요"}, torch.compile=${compile ? "켜짐" : "꺼짐"}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用已缓存的 SageAttention ([23]) 加速包（([^）]+)）$/,
    format: (m, loc) => {
      const ver = m[1]!;
      const detail = translateInner(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `Reusing cached SageAttention ${ver} acceleration package (${detail})`;
        case "zh-TW":
          return `重複使用已快取的 SageAttention ${ver} 加速套件（${detail}）`;
        case "zh-HK":
          return `複用已快取的 SageAttention ${ver} 加速套件（${detail}）`;
        case "ja-JP":
          return `キャッシュ済みの SageAttention ${ver} 高速化パッケージを再利用しています（${detail}）`;
        case "ko-KR":
          return `캐시된 SageAttention ${ver} 가속 패키지를 재사용하는 중 (${detail})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在为 (?:(Blackwell) )?(sm_\d+) 构建 SageAttention ([23]) 加速包（首次构建后将缓存复用）$/,
    format: (m, loc) => {
      const arch = m[1] ? `${m[1]} ${m[2]}` : m[2]!;
      const ver = m[3]!;
      switch (loc) {
        case "en-US":
          return `Building SageAttention ${ver} acceleration package for ${arch} (cached for reuse after first build)`;
        case "zh-TW":
          return `正在為 ${arch} 建置 SageAttention ${ver} 加速套件（首次建置後將快取重複使用）`;
        case "zh-HK":
          return `正在為 ${arch} 構建 SageAttention ${ver} 加速套件（首次構建後將快取複用）`;
        case "ja-JP":
          return `${arch} 向けに SageAttention ${ver} 高速化パッケージをビルド中（初回ビルド後はキャッシュして再利用します）`;
        case "ko-KR":
          return `${arch}용 SageAttention ${ver} 가속 패키지를 빌드하는 중 (최초 빌드 후 캐시 재사용)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^构建 SageAttention ([23]) 加速包 · (sm_\d+)$/,
    format: (m, loc) => {
      const ver = m[1]!;
      const sm = m[2]!;
      switch (loc) {
        case "en-US":
          return `Building SageAttention ${ver} package · ${sm}`;
        case "zh-TW":
          return `建置 SageAttention ${ver} 加速套件 · ${sm}`;
        case "zh-HK":
          return `構建 SageAttention ${ver} 加速套件 · ${sm}`;
        case "ja-JP":
          return `SageAttention ${ver} 高速化パッケージをビルド中 · ${sm}`;
        case "ko-KR":
          return `SageAttention ${ver} 가속 패키지 빌드 중 · ${sm}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^SageAttention ([23]) 加速包构建并安装完成（(sm_\d+)）$/,
    format: (m, loc) => {
      const ver = m[1]!;
      const sm = m[2]!;
      switch (loc) {
        case "en-US":
          return `SageAttention ${ver} acceleration package built and installed (${sm})`;
        case "zh-TW":
          return `SageAttention ${ver} 加速套件建置並安裝完成（${sm}）`;
        case "zh-HK":
          return `SageAttention ${ver} 加速套件構建並安裝完成（${sm}）`;
        case "ja-JP":
          return `SageAttention ${ver} 高速化パッケージのビルドとインストールが完了しました（${sm}）`;
        case "ko-KR":
          return `SageAttention ${ver} 가속 패키지 빌드 및 설치 완료 (${sm})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前 GPU 计算能力为 (sm_\d+)（低于 sm_80），跳过 SageAttention 加速包安装$/,
    format: (m, loc) => {
      const sm = m[1]!;
      switch (loc) {
        case "en-US":
          return `GPU compute capability is ${sm} (below sm_80); skipping SageAttention installation`;
        case "zh-TW":
          return `目前 GPU 運算能力為 ${sm}（低於 sm_80），略過 SageAttention 加速套件安裝`;
        case "zh-HK":
          return `目前 GPU 計算能力為 ${sm}（低於 sm_80），跳過 SageAttention 加速套件安裝`;
        case "ja-JP":
          return `現在の GPU コンピュート機能は ${sm}（sm_80 未満）のため、SageAttention のインストールをスキップします`;
        case "ko-KR":
          return `현재 GPU 연산 능력이 ${sm}(sm_80 미만)이므로 SageAttention 가속 패키지 설치를 건너뜁니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^未检测到满足 (?:(Blackwell) )?(sm_\d+) 要求的 nvcc（需要 CUDA >= ([\d.]+)），跳过 SageAttention ([23]) 源码构建$/,
    format: (m, loc) => {
      const arch = m[1] ? `${m[1]} ${m[2]}` : m[2]!;
      const cuda = m[3]!;
      const ver = m[4]!;
      switch (loc) {
        case "en-US":
          return `nvcc meeting ${arch} requirements not found (requires CUDA >= ${cuda}); skipping SageAttention ${ver} source build`;
        case "zh-TW":
          return `未偵測到符合 ${arch} 需求的 nvcc（需要 CUDA >= ${cuda}），略過 SageAttention ${ver} 原始碼建置`;
        case "zh-HK":
          return `未檢測到滿足 ${arch} 要求的 nvcc（需要 CUDA >= ${cuda}），跳過 SageAttention ${ver} 源碼構建`;
        case "ja-JP":
          return `${arch} の要件を満たす nvcc が見つからないため（CUDA >= ${cuda} が必要）、SageAttention ${ver} のソースビルドをスキップします`;
        case "ko-KR":
          return `${arch} 요구 사항을 충족하는 nvcc가 감지되지 않아(CUDA >= ${cuda} 필요) SageAttention ${ver} 소스 빌드를 건너뜁니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前 PyTorch ([^\s]+) 低于 2\.8，跳过 SageAttention 3 源码构建（已启用 SageAttention 2）$/,
    format: (m, loc) => {
      const ver = m[1]!;
      switch (loc) {
        case "en-US":
          return `Current PyTorch ${ver} is below 2.8; skipping SageAttention 3 source build (SageAttention 2 enabled)`;
        case "zh-TW":
          return `目前 PyTorch ${ver} 低於 2.8，略過 SageAttention 3 原始碼建置（已啟用 SageAttention 2）`;
        case "zh-HK":
          return `目前 PyTorch ${ver} 低於 2.8，跳過 SageAttention 3 源碼構建（已啟用 SageAttention 2）`;
        case "ja-JP":
          return `現在の PyTorch ${ver} は 2.8 未満のため SageAttention 3 のソースビルドをスキップします（SageAttention 2 を有効化済み）`;
        case "ko-KR":
          return `현재 PyTorch ${ver} 버전이 2.8 미만이어서 SageAttention 3 소스 빌드를 건너뜁니다 (SageAttention 2 활성화됨)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^缺少 VoxCPM2 引擎配置，无法生成内置女声参考样本：(.+)$/,
    format: (m, loc) => {
      const voice = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Missing VoxCPM2 engine configuration; cannot generate built-in female voice reference sample: ${voice}`;
        case "zh-TW":
          return `缺少 VoxCPM2 引擎設定，無法產生內建女聲參考樣本：${voice}`;
        case "zh-HK":
          return `缺少 VoxCPM2 引擎配置，無法生成內置女聲參考樣本：${voice}`;
        case "ja-JP":
          return `VoxCPM2 エンジン設定が不足しているため、組み込み女性ボイスの参照サンプルを生成できません: ${voice}`;
        case "ko-KR":
          return `VoxCPM2 엔진 설정이 없어 기본 제공 여성 음색 참조 샘플을 생성할 수 없습니다: ${voice}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^内置女声参考样本生成失败：(.+)$/,
    format: (m, loc) => {
      const voice = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to generate built-in female voice reference sample: ${voice}`;
        case "zh-TW":
          return `內建女聲參考樣本產生失敗：${voice}`;
        case "zh-HK":
          return `內置女聲參考樣本生成失敗：${voice}`;
        case "ja-JP":
          return `組み込み女性ボイスの参照サンプル生成に失敗しました: ${voice}`;
        case "ko-KR":
          return `기본 제공 여성 음색 참조 샘플 생성에 실패했습니다: ${voice}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^准备 SageAttention 算子时出现非致命异常，将自动使用当前可用注意力实现：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Non-fatal error preparing SageAttention kernels; using currently available attention implementation: ${detail}`;
        case "zh-TW":
          return `準備 SageAttention 算子時發生非致命例外，將自動使用目前可用的注意力實作：${detail}`;
        case "zh-HK":
          return `準備 SageAttention 算子時出現非致命異常，將自動使用目前可用的注意力實作：${detail}`;
        case "ja-JP":
          return `SageAttention カーネルの準備中に非致命的なエラーが発生しました。現在利用可能な Attention 実装を使用します: ${detail}`;
        case "ko-KR":
          return `SageAttention 커널 준비 중 치명적이지 않은 예외가 발생해 현재 사용 가능한 어텐션 구현을 사용합니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^第 (\d+)\/(\d+) 段 加速算子执行异常（(.+?)），已自动(.+)并重试$/s,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      const err = translateInner(m[3]!, loc);
      const reason = translateInner(m[4]!, loc);
      switch (loc) {
        case "en-US":
          return `${seg} accelerated kernel failed (${err}); automatically ${reason} and retrying`;
        case "zh-TW":
          return `${seg} 加速算子執行異常（${err}），已自動${reason}並重試`;
        case "zh-HK":
          return `${seg} 加速算子執行異常（${err}），已自動${reason}並重試`;
        case "ja-JP":
          return `${seg} の高速化カーネルでエラーが発生しました（${err}）。自動的に${reason}して再試行します`;
        case "ko-KR":
          return `${seg} 가속 커널 실행 오류 (${err}); 자동으로 ${reason} 후 재시도합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^第 (\d+)\/(\d+) 段 检测到远端 ComfyUI 已退出，正在重启引擎后继续回退重试$/,
    format: (m, loc) => {
      const seg = formatSegmentLabel(m[1]!, m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${seg} detected that remote ComfyUI exited; restarting engine before fallback retry`;
        case "zh-TW":
          return `${seg} 偵測到遠端 ComfyUI 已結束，正在重新啟動引擎後繼續回退重試`;
        case "zh-HK":
          return `${seg} 檢測到遠端 ComfyUI 已退出，正在重啟引擎後繼續回退重試`;
        case "ja-JP":
          return `${seg} でリモート ComfyUI の終了を検出しました。エンジンを再起動してからフォールバック再試行を続行します`;
        case "ko-KR":
          return `${seg}에서 원격 ComfyUI 종료를 감지했습니다. 엔진을 재시작한 후 폴백 재시도를 계속합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在为远端 GPU（([^）]+)）编译 SageAttention ([23]) 加速包（首次需 1[～~-]3 分钟，完成后自动缓存复用）$/,
    format: (m, loc) => {
      const gpu = translateInner(m[1]!, loc);
      const ver = m[2]!;
      switch (loc) {
        case "en-US":
          return `Compiling SageAttention ${ver} acceleration package for remote GPU (${gpu}) (1-3 min on first run; cached automatically for reuse)`;
        case "zh-TW":
          return `正在為遠端 GPU（${gpu}）編譯 SageAttention ${ver} 加速套件（首次需 1～3 分鐘，完成後自動快取重複使用）`;
        case "zh-HK":
          return `正在為遠端 GPU（${gpu}）編譯 SageAttention ${ver} 加速套件（首次需 1～3 分鐘，完成後自動快取複用）`;
        case "ja-JP":
          return `リモート GPU（${gpu}）向けに SageAttention ${ver} 高速化パッケージをコンパイル中（初回は 1〜3 分かかり、完了後は自動キャッシュして再利用します）`;
        case "ko-KR":
          return `원격 GPU (${gpu})용 SageAttention ${ver} 가속 패키지 컴파일 중 (최초 1-3분 소요, 완료 후 자동 캐시 재사용)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^SageAttention 加速包构建或安装跳过，将自动回退：(.+)$/s,
    format: (m, loc) => {
      const reason = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Skipped building or installing SageAttention acceleration package; falling back automatically: ${reason}`;
        case "zh-TW":
          return `已略過建置或安裝 SageAttention 加速套件，將自動回退：${reason}`;
        case "zh-HK":
          return `已跳過構建或安裝 SageAttention 加速套件，將自動回退：${reason}`;
        case "ja-JP":
          return `SageAttention 高速化パッケージのビルドまたはインストールをスキップしました。自動的にフォールバックします: ${reason}`;
        case "ko-KR":
          return `SageAttention 가속 패키지 빌드 또는 설치를 건너뛰어 자동으로 폴백합니다: ${reason}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^LongCat 加速算子执行异常，自动回退至([^：]+)：(.+)$/s,
    format: (m, loc) => {
      const target = translateLongCatFallbackTarget(m[1]!, loc);
      const detail = translateInner(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `LongCat accelerated kernel failed; automatically falling back to ${target}: ${detail}`;
        case "zh-TW":
          return `LongCat 加速算子執行異常，自動回退至 ${target}：${detail}`;
        case "zh-HK":
          return `LongCat 加速算子執行異常，自動回退至 ${target}：${detail}`;
        case "ja-JP":
          return `LongCat 高速化カーネルの実行エラーが発生したため、${target} に自動フォールバックします: ${detail}`;
        case "ko-KR":
          return `LongCat 가속 커널 실행 오류가 발생하여 ${target}(으)로 자동 폴백합니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用已缓存的内置女声参考样本：(.+)$/,
    format: (m, loc) => {
      const voice = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Reusing cached built-in female voice reference sample: ${voice}`;
        case "zh-TW":
          return `重複使用已快取的內建女聲參考樣本：${voice}`;
        case "zh-HK":
          return `複用已快取的內置女聲參考樣本：${voice}`;
        case "ja-JP":
          return `キャッシュ済みの組み込み女性音声リファレンスサンプルを再利用しています: ${voice}`;
        case "ko-KR":
          return `캐시된 기본 제공 여성 음색 참조 샘플을 재사용하는 중: ${voice}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在生成并缓存内置女声参考样本：(.+)$/,
    format: (m, loc) => {
      const voice = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Generating and caching built-in female voice reference sample: ${voice}`;
        case "zh-TW":
          return `正在產生並快取內建女聲參考樣本：${voice}`;
        case "zh-HK":
          return `正在生成並快取內置女聲參考樣本：${voice}`;
        case "ja-JP":
          return `組み込み女性音声リファレンスサンプルを生成してキャッシュしています: ${voice}`;
        case "ko-KR":
          return `기본 제공 여성 음색 참조 샘플을 생성하고 캐시하는 중: ${voice}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用已缓存的内置音色参考样本：(.+)$/,
    format: (m, loc) => {
      const voice = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Reusing cached built-in voice reference sample: ${voice}`;
        case "zh-TW":
          return `重複使用已快取的內建音色參考樣本：${voice}`;
        case "zh-HK":
          return `複用已快取的內置聲線參考樣本：${voice}`;
        case "ja-JP":
          return `キャッシュ済みの組み込みボイスリファレンスサンプルを再利用しています: ${voice}`;
        case "ko-KR":
          return `캐시된 기본 제공 음색 참조 샘플을 재사용하는 중: ${voice}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在生成并缓存内置音色参考样本：(.+)$/,
    format: (m, loc) => {
      const voice = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Generating and caching built-in voice reference sample: ${voice}`;
        case "zh-TW":
          return `正在產生並快取內建音色參考樣本：${voice}`;
        case "zh-HK":
          return `正在生成並快取內置聲線參考樣本：${voice}`;
        case "ja-JP":
          return `組み込みボイスリファレンスサンプルを生成してキャッシュしています: ${voice}`;
        case "ko-KR":
          return `기본 제공 음색 참조 샘플을 생성하고 캐시하는 중: ${voice}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^缺少 VoxCPM2 引擎配置，无法生成内置音色参考样本：(.+)$/,
    format: (m, loc) => {
      const voice = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Missing VoxCPM2 engine configuration; cannot generate built-in voice reference sample: ${voice}`;
        case "zh-TW":
          return `缺少 VoxCPM2 引擎設定，無法產生內建音色參考樣本：${voice}`;
        case "zh-HK":
          return `缺少 VoxCPM2 引擎配置，無法生成內置聲線參考樣本：${voice}`;
        case "ja-JP":
          return `VoxCPM2 エンジン設定が不足しているため、組み込みボイスの参照サンプルを生成できません: ${voice}`;
        case "ko-KR":
          return `VoxCPM2 엔진 설정이 없어 기본 제공 음색 참조 샘플을 생성할 수 없습니다: ${voice}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^内置音色参考样本生成失败：(.+)$/,
    format: (m, loc) => {
      const voice = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to generate built-in voice reference sample: ${voice}`;
        case "zh-TW":
          return `內建音色參考樣本產生失敗：${voice}`;
        case "zh-HK":
          return `內置聲線參考樣本生成失敗：${voice}`;
        case "ja-JP":
          return `組み込みボイスの参照サンプル生成に失敗しました: ${voice}`;
        case "ko-KR":
          return `기본 제공 음색 참조 샘플 생성에 실패했습니다: ${voice}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用已缓存的(?:试听音频|音色试听(?:样本)?)：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Reusing cached voice preview: ${detail}`;
        case "zh-TW":
          return `重複使用已快取的音色試聽：${detail}`;
        case "zh-HK":
          return `複用已快取的聲線試聽：${detail}`;
        case "ja-JP":
          return `キャッシュ済みのボイス試聴を再利用しています: ${detail}`;
        case "ko-KR":
          return `캐시된 음색 미리듣기를 재사용하는 중: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在合成音色试听：(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Synthesizing voice preview: ${detail}`;
        case "zh-TW":
          return `正在合成音色試聽：${detail}`;
        case "zh-HK":
          return `正在合成聲線試聽：${detail}`;
        case "ja-JP":
          return `ボイス試聴を合成中: ${detail}`;
        case "ko-KR":
          return `음색 미리듣기 합성 중: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^音色试听合成完成：(.+?)（总时长 ([\d.]+) 秒，(\d+) Hz）$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const dur = m[2]!;
      const sr = m[3]!;
      switch (loc) {
        case "en-US":
          return `Voice preview synthesis completed: ${name} (duration ${dur}s, ${sr} Hz)`;
        case "zh-TW":
          return `音色試聽合成完成：${name}（總時長 ${dur} 秒，${sr} Hz）`;
        case "zh-HK":
          return `聲線試聽合成完成：${name}（總時長 ${dur} 秒，${sr} Hz）`;
        case "ja-JP":
          return `ボイス試聴の合成が完了しました: ${name}（合計 ${dur} 秒、${sr} Hz）`;
        case "ko-KR":
          return `음색 미리듣기 합성 완료: ${name} (총 길이 ${dur}초, ${sr} Hz)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^音色试听失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Voice preview failed: ${detail}`;
        case "zh-TW":
          return `音色試聽失敗：${detail}`;
        case "zh-HK":
          return `聲線試聽失敗：${detail}`;
        case "ja-JP":
          return `ボイス試聴に失敗しました: ${detail}`;
        case "ko-KR":
          return `음색 미리듣기 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^试听音频不存在：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Preview audio not found: ${id}`;
        case "zh-TW":
          return `試聽音訊不存在：${id}`;
        case "zh-HK":
          return `試聽音訊不存在：${id}`;
        case "ja-JP":
          return `試聴音声が見つかりません: ${id}`;
        case "ko-KR":
          return `미리듣기 오디오를 찾을 수 없습니다: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^未知的内置音色：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unknown built-in voice preset: ${id}`;
        case "zh-TW":
          return `未知的內建音色：${id}`;
        case "zh-HK":
          return `未知的內置聲線：${id}`;
        case "ja-JP":
          return `不明な組み込みボイスプリセット: ${id}`;
        case "ko-KR":
          return `알 수 없는 기본 제공 음색 프리셋: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前 TTS 引擎不支持官方固定音色：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Current TTS engine does not support official fixed speaker: ${id}`;
        case "zh-TW":
          return `目前 TTS 引擎不支援官方固定音色：${id}`;
        case "zh-HK":
          return `目前 TTS 引擎不支援官方固定聲線：${id}`;
        case "ja-JP":
          return `現在の TTS エンジンは公式固定話者に対応していません: ${id}`;
        case "ko-KR":
          return `현재 TTS 엔진은 공식 기본 화자를 지원하지 않습니다: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^请求合成 TTS 音色试听：(.+)$/,
    format: (m, loc) => {
      const engine = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Requesting TTS voice preview synthesis: ${engine}`;
        case "zh-TW":
          return `要求合成 TTS 音色試聽：${engine}`;
        case "zh-HK":
          return `請求合成 TTS 聲線試聽：${engine}`;
        case "ja-JP":
          return `TTS ボイス試聴の合成をリクエスト中: ${engine}`;
        case "ko-KR":
          return `TTS 음색 미리듣기 합성 요청 중: ${engine}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端已受理 TTS 音色试听任务，但刷新状态失败（将自动重试）：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Remote accepted the TTS voice preview task, but status refresh failed (will retry automatically): ${detail}`;
        case "zh-TW":
          return `遠端已受理 TTS 音色試聽任務，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "zh-HK":
          return `遠端已受理 TTS 聲線試聽任務，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "ja-JP":
          return `リモートで TTS ボイス試聴タスクを受け付けましたが、状態の更新に失敗しました（自動再試行します）: ${detail}`;
        case "ko-KR":
          return `원격에서 TTS 음색 미리듣기 작업을 수락했지만 상태 새로고침에 실패했습니다(자동 재시도 예정): ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法的内置音色预设编号：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid built-in voice preset ID: ${id}`;
        case "zh-TW":
          return `無效的內建音色預設編號：${id}`;
        case "zh-HK":
          return `無效的內置聲線預設編號：${id}`;
        case "ja-JP":
          return `無効な組み込みボイスプリセット ID: ${id}`;
        case "ko-KR":
          return `유효하지 않은 기본 제공 음색 프리셋 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法的试听音频编号：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid preview audio ID: ${id}`;
        case "zh-TW":
          return `無效的試聽音訊編號：${id}`;
        case "zh-HK":
          return `無效的試聽音訊編號：${id}`;
        case "ja-JP":
          return `無効な試聴音声 ID: ${id}`;
        case "ko-KR":
          return `유효하지 않은 미리듣기 오디오 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前 TTS 引擎不支持该内置音色：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Current TTS engine does not support this built-in voice preset: ${id}`;
        case "zh-TW":
          return `目前 TTS 引擎不支援此內建音色：${id}`;
        case "zh-HK":
          return `目前 TTS 引擎不支援該內置聲線：${id}`;
        case "ja-JP":
          return `現在の TTS エンジンはこの組み込みボイスに対応していません: ${id}`;
        case "ko-KR":
          return `현재 TTS 엔진은 이 기본 제공 음색을 지원하지 않습니다: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 在自定义音色模式下需要上传音色参考音频才能进行语音合成$/,
    format: (m, loc) => {
      const engine = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${engine} requires uploading reference voice audio in Custom Voice mode before speech synthesis`;
        case "zh-TW":
          return `${engine} 在自訂音色模式下需要上傳音色參考音訊才能進行語音合成`;
        case "zh-HK":
          return `${engine} 在自訂聲線模式下需要上傳參考聲線音訊才能進行語音合成`;
        case "ja-JP":
          return `${engine} のカスタム音声モードでは、音声合成を行う前に参照音声のアップロードが必要です`;
        case "ko-KR":
          return `${engine}은(는) 사용자 지정 음색 모드에서 음성을 합성하려면 참조 음성 오디오를 업로드해야 합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^内置音色 ID 重复：(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Duplicate built-in voice preset ID: ${id}`;
        case "zh-TW":
          return `內建音色 ID 重複：${id}`;
        case "zh-HK":
          return `內置聲線 ID 重複：${id}`;
        case "ja-JP":
          return `組み込みボイス ID が重複しています: ${id}`;
        case "ko-KR":
          return `중복된 기본 제공 음색 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(Fish Audio Drama 3|MOSS-TTS[- ]v1\.5|Breeze TTS 2|Ming-3\.0-TTS(?: Tiny)?|IndexTTS-2\.5) 推理失败：(.+)$/s,
    format: (m, loc) => {
      const engine = m[1]!;
      const detail = translateInner(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${engine} inference failed: ${detail}`;
        case "zh-TW":
          return `${engine} 推論失敗：${detail}`;
        case "zh-HK":
          return `${engine} 推理失敗：${detail}`;
        case "ja-JP":
          return `${engine} の推論に失敗しました: ${detail}`;
        case "ko-KR":
          return `${engine} 추론 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(Fish Audio Drama 3|MOSS-TTS[- ]v1\.5|Breeze TTS 2|Ming-3\.0-TTS(?: Tiny)?|IndexTTS-2\.5) 未返回第 (\d+) 段音频$/,
    format: (m, loc) => {
      const engine = m[1]!;
      const idx = m[2]!;
      switch (loc) {
        case "en-US":
          return `${engine} did not return audio segment ${idx}`;
        case "zh-TW":
          return `${engine} 未回傳第 ${idx} 段音訊`;
        case "zh-HK":
          return `${engine} 未返回第 ${idx} 段音訊`;
        case "ja-JP":
          return `${engine} から第 ${idx} セグメント音声が返されませんでした`;
        case "ko-KR":
          return `${engine}이(가) ${idx}번째 오디오 구간을 반환하지 않았습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(Fish Audio Drama 3|MOSS-TTS[- ]v1\.5|Breeze TTS 2|Ming-3\.0-TTS(?: Tiny)?|IndexTTS-2\.5) 未生成第 (\d+) 段音频：(.+)$/,
    format: (m, loc) => {
      const engine = m[1]!;
      const idx = m[2]!;
      const path = m[3]!;
      switch (loc) {
        case "en-US":
          return `${engine} did not produce audio segment ${idx}: ${path}`;
        case "zh-TW":
          return `${engine} 未產生第 ${idx} 段音訊：${path}`;
        case "zh-HK":
          return `${engine} 未生成第 ${idx} 段音訊：${path}`;
        case "ja-JP":
          return `${engine} が第 ${idx} セグメント音声を生成しませんでした: ${path}`;
        case "ko-KR":
          return `${engine}이(가) ${idx}번째 오디오 구간을 생성하지 못했습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^在 (.+?) 中未找到 Fish Audio Drama 3 解码器权重文件$/,
    format: (m, loc) => {
      const dir = m[1]!;
      switch (loc) {
        case "en-US":
          return `Fish Audio Drama 3 decoder checkpoint was not found in ${dir}`;
        case "zh-TW":
          return `在 ${dir} 中找不到 Fish Audio Drama 3 解碼器權重檔案`;
        case "zh-HK":
          return `在 ${dir} 中未找到 Fish Audio Drama 3 解碼器權重檔案`;
        case "ja-JP":
          return `${dir} に Fish Audio Drama 3 デコーダーの重みファイルが見つかりません`;
        case "ko-KR":
          return `${dir}에서 Fish Audio Drama 3 디코더 가중치 파일을 찾을 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法参考音频素材 ID[:：]\s*(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid reference audio asset ID: ${id}`;
        case "zh-TW":
          return `無效的參考音訊素材 ID：${id}`;
        case "zh-HK":
          return `無效的參考音訊素材 ID：${id}`;
        case "ja-JP":
          return `無効な参照音声素材 ID: ${id}`;
        case "ko-KR":
          return `유효하지 않은 참조 오디오 에셋 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法情绪参考素材 ID[:：]\s*(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid emotion reference asset ID: ${id}`;
        case "zh-TW":
          return `無效的情緒參考素材 ID：${id}`;
        case "zh-HK":
          return `無效的情緒參考素材 ID：${id}`;
        case "ja-JP":
          return `無効な感情参照素材 ID: ${id}`;
        case "ko-KR":
          return `유효하지 않은 감정 참조 에셋 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在合成音色试听音频（(.+?)）$/,
    format: (m, loc) => {
      const engine = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Synthesizing voice preview audio (${engine})`;
        case "zh-TW":
          return `正在合成音色試聽音訊（${engine}）`;
        case "zh-HK":
          return `正在合成聲線試聽音訊（${engine}）`;
        case "ja-JP":
          return `ボイス試聴音声を合成中（${engine}）`;
        case "ko-KR":
          return `음색 미리듣기 오디오 합성 중 (${engine})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^音色试听音频已就绪（时长 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const dur = m[1]!;
      switch (loc) {
        case "en-US":
          return `Voice preview audio ready (${dur}s)`;
        case "zh-TW":
          return `音色試聽音訊已就緒（時長 ${dur} 秒）`;
        case "zh-HK":
          return `聲線試聽音訊已就緒（時長 ${dur} 秒）`;
        case "ja-JP":
          return `ボイス試聴音声の準備ができました（長さ ${dur} 秒）`;
        case "ko-KR":
          return `음색 미리듣기 오디오 준비 완료 (${dur}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^复用已缓存的音色试听音频（时长 ([\d.]+) 秒）$/,
    format: (m, loc) => {
      const dur = m[1]!;
      switch (loc) {
        case "en-US":
          return `Reusing cached voice preview audio (${dur}s)`;
        case "zh-TW":
          return `重複使用已快取的音色試聽音訊（時長 ${dur} 秒）`;
        case "zh-HK":
          return `複用已快取的聲線試聽音訊（時長 ${dur} 秒）`;
        case "ja-JP":
          return `キャッシュ済みのボイス試聴音声を再利用（長さ ${dur} 秒）`;
        case "ko-KR":
          return `캐시된 음색 미리듣기 오디오 재사용 (${dur}초)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Fish Audio Drama 3 至少需要 48 GB 显存（当前 ([\d.]+) GB），请切换至 G4 96 GB \/ A100 80 GB 运行时，或改用 MOSS-TTS v1\.5 \/ VoxCPM2$/,
    format: (m, loc) => {
      const vram = m[1]!;
      switch (loc) {
        case "en-US":
          return `Fish Audio Drama 3 requires at least 48 GB VRAM (current: ${vram} GB). Switch to a G4 96 GB / A100 80 GB runtime, or use MOSS-TTS v1.5 / VoxCPM2 instead`;
        case "zh-TW":
          return `Fish Audio Drama 3 至少需要 48 GB VRAM（目前 ${vram} GB），請切換至 G4 96 GB / A100 80 GB 執行階段，或改用 MOSS-TTS v1.5 / VoxCPM2`;
        case "zh-HK":
          return `Fish Audio Drama 3 至少需要 48 GB 顯存（目前 ${vram} GB），請切換至 G4 96 GB / A100 80 GB 運行時，或改用 MOSS-TTS v1.5 / VoxCPM2`;
        case "ja-JP":
          return `Fish Audio Drama 3 には少なくとも 48 GB の VRAM が必要です（現在 ${vram} GB）。G4 96 GB / A100 80 GB ランタイムに切り替えるか、MOSS-TTS v1.5 / VoxCPM2 をご利用ください`;
        case "ko-KR":
          return `Fish Audio Drama 3에는 최소 48 GB의 VRAM이 필요합니다 (현재 ${vram} GB). G4 96 GB / A100 80 GB 런타임으로 전환하거나 MOSS-TTS v1.5 / VoxCPM2를 사용해 주세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端磁盘空间不足：TTS 引擎 (.+?) 至少需要 ([\d.]+) GiB（(\d+) MiB）可用空间，当前仅剩 ([\d.]+) GiB（(\d+) MiB）$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const req = m[2]!;
      const reqMib = m[3]!;
      const avail = m[4]!;
      const availMib = m[5]!;
      switch (loc) {
        case "en-US":
          return `Insufficient remote disk space: TTS engine ${name} requires at least ${req} GiB (${reqMib} MiB) free, but only ${avail} GiB (${availMib} MiB) is available`;
        case "zh-TW":
          return `遠端磁碟空間不足：TTS 引擎 ${name} 至少需要 ${req} GiB（${reqMib} MiB）可用空間，目前僅剩 ${avail} GiB（${availMib} MiB）`;
        case "zh-HK":
          return `遠端磁碟空間不足：TTS 引擎 ${name} 至少需要 ${req} GiB（${reqMib} MiB）可用空間，目前僅餘 ${avail} GiB（${availMib} MiB）`;
        case "ja-JP":
          return `リモートディスク容量不足：TTS エンジン ${name} には少なくとも ${req} GiB（${reqMib} MiB）の空き容量が必要ですが、残り ${avail} GiB（${availMib} MiB）のみです`;
        case "ko-KR":
          return `원격 디스크 공간 부족: TTS 엔진 ${name}에는 최소 ${req} GiB(${reqMib} MiB)의 여유 공간이 필요하지만 현재 ${avail} GiB(${availMib} MiB)만 남아 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^无法检测远端磁盘空间，(.+?) 需要至少 (\d+) MiB 可用磁盘空间$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const reqMib = m[2]!;
      switch (loc) {
        case "en-US":
          return `Unable to detect remote disk space; ${name} requires at least ${reqMib} MiB free disk space`;
        case "zh-TW":
          return `無法偵測遠端磁碟空間，${name} 需要至少 ${reqMib} MiB 可用磁碟空間`;
        case "zh-HK":
          return `無法偵測遠端磁碟空間，${name} 需要至少 ${reqMib} MiB 可用磁碟空間`;
        case "ja-JP":
          return `リモートディスク容量を検出できません。${name} には少なくとも ${reqMib} MiB の空きディスク容量が必要です`;
        case "ko-KR":
          return `원격 디스크 공간을 감지할 수 없습니다. ${name}에는 최소 ${reqMib} MiB의 여유 디스크 공간이 필요합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端 TTS 引擎 (.+?) 至少需要 ([\d.]+) GiB 可用磁盘，当前仅剩 ([\d.]+) GiB$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      const req = m[2]!;
      const avail = m[3]!;
      switch (loc) {
        case "en-US":
          return `Remote TTS engine ${name} requires at least ${req} GiB free disk space, but only ${avail} GiB is available`;
        case "zh-TW":
          return `遠端 TTS 引擎 ${name} 至少需要 ${req} GiB 可用磁碟空間，目前僅剩 ${avail} GiB`;
        case "zh-HK":
          return `遠端 TTS 引擎 ${name} 至少需要 ${req} GiB 可用磁碟空間，目前僅餘 ${avail} GiB`;
        case "ja-JP":
          return `リモート TTS エンジン ${name} には少なくとも ${req} GiB の空きディスクが必要ですが、残り ${avail} GiB のみです`;
        case "ko-KR":
          return `원격 TTS 엔진 ${name}에는 최소 ${req} GiB의 여유 디스크 공간이 필요하지만 현재 ${avail} GiB만 남아 있습니다`;
        default:
          return m[0];
      }
    },
  },

  // === common/comfyui.py & console/internal/comfyui/comfyui.go extra validation ===
  {
    re: /^非法的 TTS 引擎 ID[:：]\s*(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid TTS engine ID: ${id}`;
        case "zh-TW":
          return `無效的 TTS 引擎 ID：${id}`;
        case "zh-HK":
          return `無效的 TTS 引擎 ID：${id}`;
        case "ja-JP":
          return `無効な TTS エンジン ID: ${id}`;
        case "ko-KR":
          return `유효하지 않은 TTS 엔진 ID: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^visible_when 条件 (.+?) 的值必须为字符串或布尔值$/,
    format: (m, loc) => {
      const key = m[1]!;
      switch (loc) {
        case "en-US":
          return `visible_when condition ${key} value must be a string or boolean`;
        case "zh-TW":
          return `visible_when 條件 ${key} 的值必須為字串或布林值`;
        case "zh-HK":
          return `visible_when 條件 ${key} 的值必須為字串或布林值`;
        case "ja-JP":
          return `visible_when 条件 ${key} の値は文字列または真偽値である必要があります`;
        case "ko-KR":
          return `visible_when 조건 ${key}의 값은 문자열 또는 불리언이어야 합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^分段时长必须在 ([\d.]+) 到 ([\d.]+) 秒之间$/,
    format: (m, loc) => {
      const [, lo, hi] = m;
      switch (loc) {
        case "en-US":
          return `Segment duration must be between ${lo} and ${hi} seconds`;
        case "zh-TW":
          return `分段時長必須介於 ${lo} 到 ${hi} 秒之間`;
        case "zh-HK":
          return `分段時長必須在 ${lo} 到 ${hi} 秒之間`;
        case "ja-JP":
          return `セグメント長は ${lo}〜${hi} 秒の範囲で指定してください`;
        case "ko-KR":
          return `구간 길이는 ${lo}초에서 ${hi}초 사이여야 합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^无法词法解析 multitalk\/nodes\.py：(.+)$/s,
    format: (m, loc) => {
      const detail = m[1]!;
      switch (loc) {
        case "en-US":
          return `Failed to tokenize multitalk/nodes.py: ${detail}`;
        case "zh-TW":
          return `無法進行詞法解析 multitalk/nodes.py：${detail}`;
        case "zh-HK":
          return `無法詞法解析 multitalk/nodes.py：${detail}`;
        case "ja-JP":
          return `multitalk/nodes.py の字句解析に失敗しました: ${detail}`;
        case "ko-KR":
          return `multitalk/nodes.py 어휘 분석 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模型文件名与保留控制文件冲突：(.+)$/,
    format: (m, loc) => {
      const fname = m[1]!;
      switch (loc) {
        case "en-US":
          return `Model filename conflicts with reserved control file: ${fname}`;
        case "zh-TW":
          return `模型檔案名稱與保留控制檔案衝突：${fname}`;
        case "zh-HK":
          return `模型檔案名稱與保留控制檔案衝突：${fname}`;
        case "ja-JP":
          return `モデルファイル名が予約済み制御ファイルと競合しています: ${fname}`;
        case "ko-KR":
          return `모델 파일 이름이 예약된 제어 파일과 충돌합니다: ${fname}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的 PCM WAV 位深：(.+)$/,
    format: (m, loc) => {
      const bits = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unsupported PCM WAV bit depth: ${bits}`;
        case "zh-TW":
          return `不支援的 PCM WAV 位元深度：${bits}`;
        case "zh-HK":
          return `不支援的 PCM WAV 位深：${bits}`;
        case "ja-JP":
          return `サポートされていない PCM WAV ビット深度: ${bits}`;
        case "ko-KR":
          return `지원하지 않는 PCM WAV 비트 깊이: ${bits}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在为远端 TTS 引擎创建隔离虚拟环境：(.+)$/,
    format: (m, loc) => {
      const name = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Creating isolated virtual environment for remote TTS engine: ${name}`;
        case "zh-TW":
          return `正在為遠端 TTS 引擎建立隔離虛擬環境：${name}`;
        case "zh-HK":
          return `正在為遠端 TTS 引擎建立隔離虛擬環境：${name}`;
        case "ja-JP":
          return `リモート TTS エンジン用の隔離仮想環境を作成中: ${name}`;
        case "ko-KR":
          return `원격 TTS 엔진용 격리 가상 환경 생성 중: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端缺少 ffmpeg，无法将参考音频转换为标准 WAV 格式：(.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `ffmpeg is missing on the remote runtime; cannot convert reference audio to standard WAV: ${name}`;
        case "zh-TW":
          return `遠端缺少 ffmpeg，無法將參考音訊轉換為標準 WAV 格式：${name}`;
        case "zh-HK":
          return `遠端缺少 ffmpeg，無法將參考音訊轉換為標準 WAV 格式：${name}`;
        case "ja-JP":
          return `リモート環境に ffmpeg がないため参照音声を標準 WAV 形式に変換できません: ${name}`;
        case "ko-KR":
          return `원격 런타임에 ffmpeg가 없어 참조 오디오를 표준 WAV 형식으로 변환할 수 없습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在将参考音频转码为标准 24 kHz PCM WAV：(.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Transcoding reference audio to standard 24 kHz PCM WAV: ${name}`;
        case "zh-TW":
          return `正在將參考音訊轉碼為標準 24 kHz PCM WAV：${name}`;
        case "zh-HK":
          return `正在將參考音訊轉碼為標準 24 kHz PCM WAV：${name}`;
        case "ja-JP":
          return `参照音声を標準 24 kHz PCM WAV に変換中: ${name}`;
        case "ko-KR":
          return `참조 오디오를 표준 24 kHz PCM WAV로 변환 중: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在将参考音频转码为标准 (\d+) Hz 单声道 PCM16 WAV：(.+)$/,
    format: (m, loc) => {
      const [, hz, name] = m;
      switch (loc) {
        case "en-US":
          return `Transcoding reference audio to standard ${hz} Hz mono PCM16 WAV: ${name}`;
        case "zh-TW":
          return `正在將參考音訊轉碼為標準 ${hz} Hz 單聲道 PCM16 WAV：${name}`;
        case "zh-HK":
          return `正在將參考音訊轉碼為標準 ${hz} Hz 單聲道 PCM16 WAV：${name}`;
        case "ja-JP":
          return `参照音声を標準 ${hz} Hz モノラル PCM16 WAV に変換中: ${name}`;
        case "ko-KR":
          return `참조 오디오를 표준 ${hz} Hz 모노 PCM16 WAV로 변환 중: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^参考音频转码失败，未生成输出文件：(.+)$/,
    format: (m, loc) => {
      const path = m[1]!;
      switch (loc) {
        case "en-US":
          return `Reference audio transcoding failed; output file was not created: ${path}`;
        case "zh-TW":
          return `參考音訊轉碼失敗，未產生輸出檔案：${path}`;
        case "zh-HK":
          return `參考音訊轉碼失敗，未生成輸出檔案：${path}`;
        case "ja-JP":
          return `参照音声の変換に失敗し、出力ファイルが生成されませんでした: ${path}`;
        case "ko-KR":
          return `참조 오디오 변환에 실패하여 출력 파일이 생성되지 않았습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^参考音频转码失败，未生成有效的单声道 PCM16 WAV：(.+)$/,
    format: (m, loc) => {
      const path = m[1]!;
      switch (loc) {
        case "en-US":
          return `Reference audio transcoding failed; no valid mono PCM16 WAV was created: ${path}`;
        case "zh-TW":
          return `參考音訊轉碼失敗，未產生有效的單聲道 PCM16 WAV：${path}`;
        case "zh-HK":
          return `參考音訊轉碼失敗，未生成有效的單聲道 PCM16 WAV：${path}`;
        case "ja-JP":
          return `参照音声の変換に失敗し、有効なモノラル PCM16 WAV が生成されませんでした: ${path}`;
        case "ko-KR":
          return `참조 오디오 변환에 실패하여 유효한 모노 PCM16 WAV가 생성되지 않았습니다: ${path}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 参数格式无效：不能为布尔值$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Invalid ${label} parameter format: boolean is not allowed`;
        case "zh-TW":
          return `${label} 參數格式無效：不可為布林值`;
        case "zh-HK":
          return `${label} 參數格式無效：不能為布林值`;
        case "ja-JP":
          return `${label} パラメータ形式が無効です: 真偽値は指定できません`;
        case "ko-KR":
          return `${label} 매개변수 형식이 올바르지 않습니다: 불리언 값은 허용되지 않습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 参数格式无效：(.+)$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      const raw = m[2]!;
      switch (loc) {
        case "en-US":
          return `Invalid ${label} parameter format: ${raw}`;
        case "zh-TW":
          return `${label} 參數格式無效：${raw}`;
        case "zh-HK":
          return `${label} 參數格式無效：${raw}`;
        case "ja-JP":
          return `${label} パラメータ形式が無効です: ${raw}`;
        case "ko-KR":
          return `${label} 매개변수 형식이 올바르지 않습니다: ${raw}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 必须为有限数值$/,
    format: (m, loc) => {
      const label = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} must be a finite number`;
        case "zh-TW":
          return `${label} 必須為有限數值`;
        case "zh-HK":
          return `${label} 必須為有限數值`;
        case "ja-JP":
          return `${label} は有限の数値である必要があります`;
        case "ko-KR":
          return `${label}은(는) 유한한 숫자여야 합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^输出文件格式与类型不匹配：(.+)$/,
    format: (m, loc) => {
      const ext = m[1]!;
      switch (loc) {
        case "en-US":
          return `Output file format does not match media type: ${ext}`;
        case "zh-TW":
          return `輸出檔案格式與類型不相符：${ext}`;
        case "zh-HK":
          return `輸出檔案格式與類型不匹配：${ext}`;
        case "ja-JP":
          return `出力ファイル形式と種別が一致しません: ${ext}`;
        case "ko-KR":
          return `출력 파일 형식이 유형과 일치하지 않습니다: ${ext}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^节点补丁引用了未在 custom_nodes 中声明的节点[:：]\s*(.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Node patch references a node not declared in custom_nodes: ${name}`;
        case "zh-TW":
          return `節點修補參考了未在 custom_nodes 中宣告的節點：${name}`;
        case "zh-HK":
          return `節點補丁引用了未在 custom_nodes 中聲明的節點：${name}`;
        case "ja-JP":
          return `ノードパッチが custom_nodes に未定義のノードを参照しています: ${name}`;
        case "ko-KR":
          return `노드 패치가 custom_nodes에 선언되지 않은 노드를 참조합니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^节点补丁引用的自定义节点不存在[:：]\s*(.+)$/,
    format: (m, loc) => {
      const name = m[1]!;
      switch (loc) {
        case "en-US":
          return `Custom node referenced by node patch does not exist: ${name}`;
        case "zh-TW":
          return `節點修補參考的自訂節點不存在：${name}`;
        case "zh-HK":
          return `節點補丁引用的自訂節點不存在：${name}`;
        case "ja-JP":
          return `ノードパッチが参照するカスタムノードが存在しません: ${name}`;
        case "ko-KR":
          return `노드 패치가 참조하는 사용자 지정 노드가 존재하지 않습니다: ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模板 (.+?) 的默认 TTS 引擎未在 tts_engines 中声明[:：]\s*(.+)$/,
    format: (m, loc) => {
      const [, tmplId, engineId] = m;
      switch (loc) {
        case "en-US":
          return `Template ${tmplId} default TTS engine is not declared in tts_engines: ${engineId}`;
        case "zh-TW":
          return `範本 ${tmplId} 的預設 TTS 引擎未在 tts_engines 中宣告：${engineId}`;
        case "zh-HK":
          return `範本 ${tmplId} 的預設 TTS 引擎未在 tts_engines 中聲明：${engineId}`;
        case "ja-JP":
          return `テンプレート ${tmplId} のデフォルト TTS エンジンが tts_engines に定義されていません: ${engineId}`;
        case "ko-KR":
          return `템플릿 ${tmplId}의 기본 TTS 엔진이 tts_engines에 선언되지 않았습니다: ${engineId}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^模板 (.+?) 引用了未在 tts_engines 中声明的 TTS 引擎[:：]\s*(.+)$/,
    format: (m, loc) => {
      const [, tmplId, engineId] = m;
      switch (loc) {
        case "en-US":
          return `Template ${tmplId} references a TTS engine not declared in tts_engines: ${engineId}`;
        case "zh-TW":
          return `範本 ${tmplId} 參考了未在 tts_engines 中宣告的 TTS 引擎：${engineId}`;
        case "zh-HK":
          return `範本 ${tmplId} 引用了未在 tts_engines 中聲明的 TTS 引擎：${engineId}`;
        case "ja-JP":
          return `テンプレート ${tmplId} が tts_engines に未定義の TTS エンジンを参照しています: ${engineId}`;
        case "ko-KR":
          return `템플릿 ${tmplId}이(가) tts_engines에 선언되지 않은 TTS 엔진을 참조합니다: ${engineId}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流模板 (.+?) 引用了未定义的默认 TTS 引擎[:：]\s*(.+)$/,
    format: (m, loc) => {
      const [, tmplId, engineId] = m;
      switch (loc) {
        case "en-US":
          return `Workflow template ${tmplId} references an undefined default TTS engine: ${engineId}`;
        case "zh-TW":
          return `工作流程範本 ${tmplId} 參考了未定義的預設 TTS 引擎：${engineId}`;
        case "zh-HK":
          return `工作流範本 ${tmplId} 引用了未定義的預設 TTS 引擎：${engineId}`;
        case "ja-JP":
          return `ワークフローテンプレート ${tmplId} が未定義のデフォルト TTS エンジンを参照しています: ${engineId}`;
        case "ko-KR":
          return `워크플로 템플릿 ${tmplId}이(가) 정의되지 않은 기본 TTS 엔진을 참조합니다: ${engineId}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作流模板 (.+?) 引用了未定义的 TTS 引擎[:：]\s*(.+)$/,
    format: (m, loc) => {
      const [, tmplId, engineId] = m;
      switch (loc) {
        case "en-US":
          return `Workflow template ${tmplId} references an undefined TTS engine: ${engineId}`;
        case "zh-TW":
          return `工作流程範本 ${tmplId} 參考了未定義的 TTS 引擎：${engineId}`;
        case "zh-HK":
          return `工作流範本 ${tmplId} 引用了未定義的 TTS 引擎：${engineId}`;
        case "ja-JP":
          return `ワークフローテンプレート ${tmplId} が未定義の TTS エンジンを参照しています: ${engineId}`;
        case "ko-KR":
          return `워크플로 템플릿 ${tmplId}이(가) 정의되지 않은 TTS 엔진을 참조합니다: ${engineId}`;
        default:
          return m[0];
      }
    },
  },

  // === Worker-file containment, validation, TTS/accel/context, updater & proxy rules ===
  {
    re: /^(.+?)工作目录安全校验失败$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!.trim(), loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} working directory safety validation failed`;
        case "zh-TW":
          return `${label}${zhSep}工作目錄安全驗證失敗`;
        case "zh-HK":
          return `${label}${zhSep}工作目錄安全校驗失敗`;
        case "ja-JP":
          return `${label} の作業ディレクトリ安全性検証に失敗しました`;
        case "ko-KR":
          return `${label} 작업 디렉터리 보안 검증에 실패했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)路径超出工作目录安全范围$/,
    format: (m, loc) => {
      const raw = m[1]!.trim();
      const baseRaw =
        raw.endsWith("路径") && raw.length > 2 ? raw.slice(0, -2) : raw;
      const label = translateSymlinkDirLabel(baseRaw, loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} path is outside the working directory safety boundary`;
        case "zh-TW":
          return `${label}${zhSep}路徑超出工作目錄安全範圍`;
        case "zh-HK":
          return `${label}${zhSep}路徑超出工作目錄安全範圍`;
        case "ja-JP":
          return `${label} のパスが作業ディレクトリの安全範囲外です`;
        case "ko-KR":
          return `${label} 경로가 작업 디렉터리 안전 범위를 벗어났습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)路径包含符号链接，安全校验失败$/,
    format: (m, loc) => {
      const raw = m[1]!.trim();
      const baseRaw =
        raw.endsWith("路径") && raw.length > 2 ? raw.slice(0, -2) : raw;
      const label = translateSymlinkDirLabel(baseRaw, loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} path contains a symbolic link; safety validation failed`;
        case "zh-TW":
          return `${label}${zhSep}路徑包含符號連結，安全驗證失敗`;
        case "zh-HK":
          return `${label}${zhSep}路徑包含符號連結，安全校驗失敗`;
        case "ja-JP":
          return `${label} のパスにシンボリックリンクが含まれているため安全性検証に失敗しました`;
        case "ko-KR":
          return `${label} 경로에 심볼릭 링크가 포함되어 보안 검증에 실패했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)路径安全校验失败$/,
    format: (m, loc) => {
      const raw = m[1]!.trim();
      const baseRaw =
        raw.endsWith("路径") && raw.length > 2 ? raw.slice(0, -2) : raw;
      const label = translateSymlinkDirLabel(baseRaw, loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} path failed safety validation`;
        case "zh-TW":
          return `${label}${zhSep}路徑安全驗證失敗`;
        case "zh-HK":
          return `${label}${zhSep}路徑安全校驗失敗`;
        case "ja-JP":
          return `${label} のパス安全性検証に失敗しました`;
        case "ko-KR":
          return `${label} 경로 보안 검증에 실패했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^下拉字段 (.+?) 的默认值不在 options 列表中$/,
    format: (m, loc) => {
      const field = m[1]!;
      switch (loc) {
        case "en-US":
          return `Default value of select field ${field} is not in the options list`;
        case "zh-TW":
          return `下拉欄位 ${field} 的預設值不在 options 清單中`;
        case "zh-HK":
          return `下拉欄位 ${field} 的預設值不在 options 列表中`;
        case "ja-JP":
          return `セレクトフィールド ${field} のデフォルト値が options リストに含まれていません`;
        case "ko-KR":
          return `선택 필드 ${field}의 기본값이 options 목록에 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^字段 (.+?) 的文本长度不能超过 (\d+) 个字符$/,
    format: (m, loc) => {
      const [, field, limit] = m;
      switch (loc) {
        case "en-US":
          return `Text length of field ${field} cannot exceed ${limit} characters`;
        case "zh-TW":
          return `欄位 ${field} 的文字長度不可超過 ${limit} 個字元`;
        case "zh-HK":
          return `欄位 ${field} 的文字長度不能超過 ${limit} 個字元`;
        case "ja-JP":
          return `フィールド ${field} のテキスト長は ${limit} 文字を超えられません`;
        case "ko-KR":
          return `필드 ${field}의 텍스트 길이는 ${limit}자를 초과할 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法的 GitHub 仓库地址：(.+)$/,
    format: (m, loc) => {
      const url = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid GitHub repository URL: ${url}`;
        case "zh-TW":
          return `無效的 GitHub 儲存庫網址：${url}`;
        case "zh-HK":
          return `無效的 GitHub 倉庫地址：${url}`;
        case "ja-JP":
          return `無効な GitHub リポジトリ URL です: ${url}`;
        case "ko-KR":
          return `유효하지 않은 GitHub 저장소 주소입니다: ${url}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^TTS 引擎源码仓库仅允许使用 https:\/\/github\.com\/<owner>\/<repo> 地址：(.+)$/,
    format: (m, loc) => {
      const url = m[1]!;
      switch (loc) {
        case "en-US":
          return `TTS engine source repository must use an https://github.com/<owner>/<repo> URL: ${url}`;
        case "zh-TW":
          return `TTS 引擎原始碼儲存庫僅允許使用 https://github.com/<owner>/<repo> 網址：${url}`;
        case "zh-HK":
          return `TTS 引擎源碼倉庫僅允許使用 https://github.com/<owner>/<repo> 地址：${url}`;
        case "ja-JP":
          return `TTS エンジンのソースリポジトリには https://github.com/<owner>/<repo> 形式の URL のみ指定できます: ${url}`;
        case "ko-KR":
          return `TTS 엔진 소스 저장소는 https://github.com/<owner>/<repo> 형식의 주소만 허용됩니다: ${url}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Ming-3\.0-TTS 未生成参考锚点音频：(.+)$/,
    format: (m, loc) => {
      const detail = m[1]!;
      switch (loc) {
        case "en-US":
          return `Ming-3.0-TTS did not generate reference anchor audio: ${detail}`;
        case "zh-TW":
          return `Ming-3.0-TTS 未產生參考錨點音訊：${detail}`;
        case "zh-HK":
          return `Ming-3.0-TTS 未生成參考錨點音訊：${detail}`;
        case "ja-JP":
          return `Ming-3.0-TTS が参照アンカー音声を生成しませんでした: ${detail}`;
        case "ko-KR":
          return `Ming-3.0-TTS가 참조 앵커 오디오를 생성하지 않았습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^加速包源文件不合法：(.+)$/,
    format: (m, loc) => {
      const detail = m[1]!;
      switch (loc) {
        case "en-US":
          return `Invalid accelerator package source file: ${detail}`;
        case "zh-TW":
          return `加速套件來源檔案無效：${detail}`;
        case "zh-HK":
          return `加速包來源檔案不合法：${detail}`;
        case "ja-JP":
          return `高速化パッケージのソースファイルが無効です: ${detail}`;
        case "ko-KR":
          return `가속 패키지 소스 파일이 올바르지 않습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端命令执行失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Remote command execution failed: ${detail}`;
        case "zh-TW":
          return `遠端命令執行失敗：${detail}`;
        case "zh-HK":
          return `遠端命令執行失敗：${detail}`;
        case "ja-JP":
          return `リモートコマンドの実行に失敗しました: ${detail}`;
        case "ko-KR":
          return `원격 명령 실행에 실패했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的模型文件扩展名：(.+)$/,
    format: (m, loc) => {
      const ext = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unsupported model file extension: ${ext}`;
        case "zh-TW":
          return `不支援的模型副檔名：${ext}`;
        case "zh-HK":
          return `不支援的模型檔案副檔名：${ext}`;
        case "ja-JP":
          return `未対応のモデルファイル拡張子です: ${ext}`;
        case "ko-KR":
          return `지원하지 않는 모델 파일 확장자입니다: ${ext}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^检测到生成阶段耗尽上下文上限（(\d+) tokens），已自动提高后续请求预留输出空间（\+(\d+) tokens）$/,
    format: (m, loc) => {
      const [, ctxTokens, extraTokens] = m;
      switch (loc) {
        case "en-US":
          return `Detected generation hitting the context limit (${ctxTokens} tokens); automatically increased reserved output budget for subsequent requests (+${extraTokens} tokens)`;
        case "zh-TW":
          return `偵測到產生階段耗盡內容上限（${ctxTokens} tokens），已自動提高後續請求預留輸出空間（+${extraTokens} tokens）`;
        case "zh-HK":
          return `檢測到生成階段耗盡上下文上限（${ctxTokens} tokens），已自動提高後續請求預留輸出空間（+${extraTokens} tokens）`;
        case "ja-JP":
          return `生成フェーズでコンテキスト上限（${ctxTokens} tokens）に達したため、以降のレスポンス予約出力枠を自動で拡張しました（+${extraTokens} tokens）`;
        case "ko-KR":
          return `생성 단계에서 컨텍스트 상한(${ctxTokens} tokens) 소진이 감지되어 이후 요청의 출력 예약 공간을 자동으로 늘렸습니다 (+${extraTokens} tokens)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^ColabOne (v[\w.-]+) 更新已就绪，重启客户端后生效$/,
    format: (m, loc) => {
      const ver = m[1]!;
      switch (loc) {
        case "en-US":
          return `ColabOne ${ver} update is ready and will take effect after restarting the client`;
        case "zh-TW":
          return `ColabOne ${ver} 更新已就緒，重新啟動用戶端後生效`;
        case "zh-HK":
          return `ColabOne ${ver} 更新已就緒，重啟用戶端後生效`;
        case "ja-JP":
          return `ColabOne ${ver} のアップデート準備が完了しました。クライアント再起動後に適用されます`;
        case "ko-KR":
          return `ColabOne ${ver} 업데이트가 준비되었습니다. 클라이언트를 재시작하면 적용됩니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(后台)?检查客户端更新失败（([^）]+)）：(.+)$/s,
    format: (m, loc) => {
      const isBackground = Boolean(m[1]);
      const reason = translateInner(m[2]!, loc);
      const detail = translateInner(m[3]!, loc);
      switch (loc) {
        case "en-US":
          return isBackground
            ? `Background client update check failed (${reason}): ${detail}`
            : `Client update check failed (${reason}): ${detail}`;
        case "zh-TW":
          return isBackground
            ? `背景檢查用戶端更新失敗（${reason}）：${detail}`
            : `檢查用戶端更新失敗（${reason}）：${detail}`;
        case "zh-HK":
          return isBackground
            ? `後台檢查用戶端更新失敗（${reason}）：${detail}`
            : `檢查用戶端更新失敗（${reason}）：${detail}`;
        case "ja-JP":
          return isBackground
            ? `バックグラウンドでのクライアント更新確認に失敗しました（${reason}）: ${detail}`
            : `クライアント更新の確認に失敗しました（${reason}）: ${detail}`;
        case "ko-KR":
          return isBackground
            ? `백그라운드 클라이언트 업데이트 확인에 실패했습니다 (${reason}): ${detail}`
            : `클라이언트 업데이트 확인에 실패했습니다 (${reason}): ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(后台)?检查客户端更新失败：(.+)$/s,
    format: (m, loc) => {
      const isBackground = Boolean(m[1]);
      const detail = translateInner(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return isBackground
            ? `Background client update check failed: ${detail}`
            : `Client update check failed: ${detail}`;
        case "zh-TW":
          return isBackground
            ? `背景檢查用戶端更新失敗：${detail}`
            : `檢查用戶端更新失敗：${detail}`;
        case "zh-HK":
          return isBackground
            ? `後台檢查用戶端更新失敗：${detail}`
            : `檢查用戶端更新失敗：${detail}`;
        case "ja-JP":
          return isBackground
            ? `バックグラウンドでのクライアント更新確認に失敗しました: ${detail}`
            : `クライアント更新の確認に失敗しました: ${detail}`;
        case "ko-KR":
          return isBackground
            ? `백그라운드 클라이언트 업데이트 확인에 실패했습니다: ${detail}`
            : `클라이언트 업데이트 확인에 실패했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^SOCKS5 代理连接失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `SOCKS5 proxy connection failed: ${detail}`;
        case "zh-TW":
          return `SOCKS5 代理連線失敗：${detail}`;
        case "zh-HK":
          return `SOCKS5 代理連線失敗：${detail}`;
        case "ja-JP":
          return `SOCKS5 プロキシへの接続に失敗しました: ${detail}`;
        case "ko-KR":
          return `SOCKS5 프록시 연결에 실패했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^代理拒绝连接 \(HTTP (\d+)\)$/,
    format: (m, loc) => {
      const code = m[1]!;
      switch (loc) {
        case "en-US":
          return `Proxy refused connection (HTTP ${code})`;
        case "zh-TW":
          return `代理拒絕連線 (HTTP ${code})`;
        case "zh-HK":
          return `代理拒絕連線 (HTTP ${code})`;
        case "ja-JP":
          return `プロキシが接続を拒否しました (HTTP ${code})`;
        case "ko-KR":
          return `프록시가 연결을 거부했습니다 (HTTP ${code})`;
        default:
          return m[0];
      }
    },
  },

  // === server/comfyui.py WebSocket live progress (placed last so specific · rules run first) ===
  {
    re: /^(.+) · 采样 (\d+)\/(\d+)$/,
    format: (m, loc) => {
      const head = translateInner(m[1]!, loc);
      const step = m[2]!;
      const maxSteps = m[3]!;
      switch (loc) {
        case "en-US":
          return `${head} · Step ${step}/${maxSteps}`;
        case "zh-TW":
          return `${head} · 取樣 ${step}/${maxSteps}`;
        case "zh-HK":
          return `${head} · 採樣 ${step}/${maxSteps}`;
        case "ja-JP":
          return `${head} · ステップ ${step}/${maxSteps}`;
        case "ko-KR":
          return `${head} · 스텝 ${step}/${maxSteps}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+) · ([^·]+)$/,
    when: (text) => {
      const idx = text.lastIndexOf(" · ");
      if (idx <= 0) return false;
      const left = text.slice(0, idx);
      const right = text.slice(idx + 3);
      if (
        !right ||
        right.includes("·") ||
        /[\r\n]/.test(right) ||
        UNHANDLED_PROGRESS_TAIL_RE.test(right)
      ) {
        return false;
      }
      if (COMFY_WS_NODE_OR_PHASE_LABELS.has(right)) {
        return true;
      }
      return (
        COMFY_WORKFLOW_NAMES.has(left) &&
        !/[：:]/.test(right) &&
        !/[\u3400-\u9fff\uf900-\ufaff]/.test(right)
      );
    },
    format: (m, loc) => {
      const wfName = translateInner(m[1]!, loc);
      const nodeOrPhase = translateInner(m[2]!, loc);
      return `${wfName} · ${nodeOrPhase}`;
    },
  },
  {
    re: /^已导出加密迁移文件（(\d+) 项模块）$/,
    format: (m, loc) => {
      const count = m[1]!;
      switch (loc) {
        case "en-US":
          return `Exported encrypted migration archive (${count} modules)`;
        case "zh-TW":
          return `已加密匯出遷移檔案（${count} 項模組）`;
        case "zh-HK":
          return `已加密匯出遷移檔案（${count} 項模組）`;
        case "ja-JP":
          return `暗号化移行ファイルをエクスポートしました（${count} モジュール）`;
        case "ko-KR":
          return `암호화된 마이그레이션 파일을 내보냈습니다 (${count}개 모듈)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已导入迁移配置（(.+)）$/,
    format: (m, loc) => {
      const rawList = m[1]!;
      const modMap: Record<string, ColabOneLocaleTable> = {
        "Google 账号与云盘认证": {
          "zh-CN": "Google 账号与云盘认证",
          "en-US": "Google Account & Drive Auth",
          "zh-TW": "Google 帳號與雲端硬碟認證",
          "zh-HK": "Google 帳號與雲端硬碟認證",
          "ja-JP": "Google アカウントとドライブ認証",
          "ko-KR": "Google 계정 및 드라이브 인증",
        },
        第三方密钥与公网隧道: {
          "zh-CN": "第三方密钥与公网隧道",
          "en-US": "Third-Party Keys & Tunnels",
          "zh-TW": "第三方金鑰與公網隧道",
          "zh-HK": "第三方金鑰與公網隧道",
          "ja-JP": "外部キーと公開トンネル",
          "ko-KR": "외부 키 및 공개 터널",
        },
        软件与外观设置: {
          "zh-CN": "软件与外观设置",
          "en-US": "App & Appearance Settings",
          "zh-TW": "軟體與外觀設定",
          "zh-HK": "軟件與外觀設定",
          "ja-JP": "アプリ・外観設定",
          "ko-KR": "앱 및 화면 설정",
        },
        模型库与自定义工作流: {
          "zh-CN": "模型库与自定义工作流",
          "en-US": "Model Library & Workflows",
          "zh-TW": "模型庫與自訂工作流程",
          "zh-HK": "模型庫與自訂工作流",
          "ja-JP": "モデルライブラリとワークフロー",
          "ko-KR": "모델 라이브러리 및 워크플로",
        },
        "CU 算力消耗历史": {
          "zh-CN": "CU 算力消耗历史",
          "en-US": "CU Usage History",
          "zh-TW": "CU 算力消耗歷史",
          "zh-HK": "CU 算力消耗歷史",
          "ja-JP": "CU 使用履歴",
          "ko-KR": "CU 사용 기록",
        },
        "ComfyUI 文本草稿": {
          "zh-CN": "ComfyUI 文本草稿",
          "en-US": "ComfyUI Text Drafts",
          "zh-TW": "ComfyUI 文字草稿",
          "zh-HK": "ComfyUI 文字草稿",
          "ja-JP": "ComfyUI テキスト下書き",
          "ko-KR": "ComfyUI 텍스트 초안",
        },
      };
      const sep = loc === "en-US" || loc === "ko-KR" ? ", " : "、";
      const items = rawList
        .split("、")
        .map((part) => modMap[part.trim()]?.[loc] || part.trim())
        .join(sep);
      switch (loc) {
        case "en-US":
          return `Imported migration configuration (${items})`;
        case "zh-TW":
          return `已匯入遷移設定（${items}）`;
        case "zh-HK":
          return `已匯入遷移配置（${items}）`;
        case "ja-JP":
          return `移行設定をインポートしました（${items}）`;
        case "ko-KR":
          return `마이그레이션 구성을 가져왔습니다 (${items})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的迁移模块：(.+)$/,
    format: (m, loc) => {
      const mod = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unsupported migration module: ${mod}`;
        case "zh-TW":
          return `不支援的遷移模組：${mod}`;
        case "zh-HK":
          return `不支援的遷移模組：${mod}`;
        case "ja-JP":
          return `サポートされていない移行モジュールです: ${mod}`;
        case "ko-KR":
          return `지원되지 않는 마이그레이션 모듈입니다: ${mod}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^迁移文件中的\s*(Hugging Face Token|分配代理地址|自定义模型配置|模型 API 别名|自定义工作流|工作流草稿)\s*无效：(.+)$/,
    format: (m, loc) => {
      const targetMap: Record<string, ColabOneLocaleTable> = {
        "Hugging Face Token": {
          "zh-CN": "Hugging Face Token",
          "en-US": "Hugging Face token",
          "zh-TW": "Hugging Face Token",
          "zh-HK": "Hugging Face Token",
          "ja-JP": "Hugging Face トークン",
          "ko-KR": "Hugging Face 토큰",
        },
        分配代理地址: {
          "zh-CN": "分配代理地址",
          "en-US": "allocation proxy URL",
          "zh-TW": "分配代理位址",
          "zh-HK": "分配代理地址",
          "ja-JP": "割り当てプロキシ URL",
          "ko-KR": "할당 프록시 URL",
        },
        自定义模型配置: {
          "zh-CN": "自定义模型配置",
          "en-US": "custom model configuration",
          "zh-TW": "自訂模型設定",
          "zh-HK": "自訂模型配置",
          "ja-JP": "カスタムモデル設定",
          "ko-KR": "사용자 지정 모델 설정",
        },
        "模型 API 别名": {
          "zh-CN": "模型 API 别名",
          "en-US": "model API alias",
          "zh-TW": "模型 API 別名",
          "zh-HK": "模型 API 別名",
          "ja-JP": "モデル API エイリアス",
          "ko-KR": "모델 API 별칭",
        },
        自定义工作流: {
          "zh-CN": "自定义工作流",
          "en-US": "custom workflow",
          "zh-TW": "自訂工作流程",
          "zh-HK": "自訂工作流",
          "ja-JP": "カスタムワークフロー",
          "ko-KR": "사용자 지정 워크플로",
        },
        工作流草稿: {
          "zh-CN": "工作流草稿",
          "en-US": "workflow draft",
          "zh-TW": "工作流程草稿",
          "zh-HK": "工作流草稿",
          "ja-JP": "ワークフロー下書き",
          "ko-KR": "워크플로 초안",
        },
      };
      const label = targetMap[m[1]!]?.[loc] || m[1]!;
      const detail = translateInner(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `Invalid ${label} in migration archive: ${detail}`;
        case "zh-TW":
          return `遷移檔案中的${label}無效：${detail}`;
        case "zh-HK":
          return `遷移檔案中的${label}無效：${detail}`;
        case "ja-JP":
          return `移行ファイル内の${label}が無効です: ${detail}`;
        case "ko-KR":
          return `마이그레이션 파일의 ${label}이(가) 올바르지 않습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^写入\s*(Google 登录凭据|Google 账号资料|Google 云盘项目配置|Hugging Face Token|分配代理配置|对外 API 配置|ComfyUI 画布配置|软件设置|模型库配置|自定义工作流|ComfyUI 草稿|CU 算力消耗历史)\s*失败：(.+)$/,
    format: (m, loc) => {
      const targetMap: Record<string, ColabOneLocaleTable> = {
        "Google 登录凭据": {
          "zh-CN": "Google 登录凭据",
          "en-US": "Google login credentials",
          "zh-TW": "Google 登入憑證",
          "zh-HK": "Google 登入憑證",
          "ja-JP": "Google ログイン認証情報",
          "ko-KR": "Google 로그인 자격 증명",
        },
        "Google 账号资料": {
          "zh-CN": "Google 账号资料",
          "en-US": "Google account profile",
          "zh-TW": "Google 帳號資料",
          "zh-HK": "Google 帳號資料",
          "ja-JP": "Google アカウント情報",
          "ko-KR": "Google 계정 프로필",
        },
        "Google 云盘项目配置": {
          "zh-CN": "Google 云盘项目配置",
          "en-US": "Google Drive quota project",
          "zh-TW": "Google 雲端硬碟專案設定",
          "zh-HK": "Google 雲端硬碟專案配置",
          "ja-JP": "Google ドライブのクォータプロジェクト設定",
          "ko-KR": "Google 드라이브 할당 프로젝트 설정",
        },
        "Hugging Face Token": {
          "zh-CN": "Hugging Face Token",
          "en-US": "Hugging Face token",
          "zh-TW": "Hugging Face Token",
          "zh-HK": "Hugging Face Token",
          "ja-JP": "Hugging Face トークン",
          "ko-KR": "Hugging Face 토큰",
        },
        分配代理配置: {
          "zh-CN": "分配代理配置",
          "en-US": "allocation proxy settings",
          "zh-TW": "分配代理設定",
          "zh-HK": "分配代理配置",
          "ja-JP": "割り当てプロキシ設定",
          "ko-KR": "할당 프록시 설정",
        },
        "对外 API 配置": {
          "zh-CN": "对外 API 配置",
          "en-US": "Public API settings",
          "zh-TW": "對外 API 設定",
          "zh-HK": "對外 API 配置",
          "ja-JP": "外部 API 設定",
          "ko-KR": "외부 API 설정",
        },
        "ComfyUI 画布配置": {
          "zh-CN": "ComfyUI 画布配置",
          "en-US": "ComfyUI Canvas settings",
          "zh-TW": "ComfyUI 畫布設定",
          "zh-HK": "ComfyUI 畫布配置",
          "ja-JP": "ComfyUI キャンバス設定",
          "ko-KR": "ComfyUI 캔버스 설정",
        },
        软件设置: {
          "zh-CN": "软件设置",
          "en-US": "app settings",
          "zh-TW": "軟體設定",
          "zh-HK": "軟件設定",
          "ja-JP": "アプリ設定",
          "ko-KR": "앱 설정",
        },
        模型库配置: {
          "zh-CN": "模型库配置",
          "en-US": "model library",
          "zh-TW": "模型庫設定",
          "zh-HK": "模型庫配置",
          "ja-JP": "モデルライブラリ設定",
          "ko-KR": "모델 라이브러리 설정",
        },
        自定义工作流: {
          "zh-CN": "自定义工作流",
          "en-US": "custom workflows",
          "zh-TW": "自訂工作流程",
          "zh-HK": "自訂工作流",
          "ja-JP": "カスタムワークフロー",
          "ko-KR": "사용자 지정 워크플로",
        },
        "ComfyUI 草稿": {
          "zh-CN": "ComfyUI 草稿",
          "en-US": "ComfyUI drafts",
          "zh-TW": "ComfyUI 草稿",
          "zh-HK": "ComfyUI 草稿",
          "ja-JP": "ComfyUI 下書き",
          "ko-KR": "ComfyUI 초안",
        },
        "CU 算力消耗历史": {
          "zh-CN": "CU 算力消耗历史",
          "en-US": "CU usage history",
          "zh-TW": "CU 算力消耗歷史",
          "zh-HK": "CU 算力消耗歷史",
          "ja-JP": "CU 使用履歴",
          "ko-KR": "CU 사용 기록",
        },
      };
      const label = targetMap[m[1]!]?.[loc] || m[1]!;
      const detail = translateInner(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to write ${label}: ${detail}`;
        case "zh-TW":
          return `寫入${label}失敗：${detail}`;
        case "zh-HK":
          return `寫入${label}失敗：${detail}`;
        case "ja-JP":
          return `${label}の書き込みに失敗しました: ${detail}`;
        case "ko-KR":
          return `${label} 저장에 실패했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^终稿体积 ([\d.]+) MiB 超过 (\d+) MiB 上限，正在按目标视频码率 (\d+) kbps 自动重编码一次以保留本次渲染成果$/,
    format: (m, loc) => {
      const [, size, limit, kbps] = m;
      switch (loc) {
        case "en-US":
          return `Final video size (${size} MiB) exceeds the ${limit} MiB limit; automatically re-encoding once at ${kbps} kbps target video bitrate to preserve this render`;
        case "zh-TW":
          return `終稿大小 ${size} MiB 超過 ${limit} MiB 上限，正在依目標影片位元率 ${kbps} kbps 自動重新編碼一次以保留本次算繪成果`;
        case "zh-HK":
          return `終稿體積 ${size} MiB 超過 ${limit} MiB 上限，正在按目標影片碼率 ${kbps} kbps 自動重新編碼一次以保留本次渲染成果`;
        case "ja-JP":
          return `最終出力サイズ（${size} MiB）が上限の ${limit} MiB を超えたため、レンダリング結果を保持するため目標映像ビットレート ${kbps} kbps で自動再エンコードしています`;
        case "ko-KR":
          return `최종 영상 크기(${size} MiB)가 ${limit} MiB 상한을 초과하여 이번 렌더링 결과를 유지하기 위해 목표 비디오 비트레이트 ${kbps} kbps로 1회 자동 재인코딩하는 중`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端磁盘空间不足：TTS 引擎 (.+?)（约 ([\d.]+) GiB）与 LongCat-Video-Avatar 1\.6 原生管线（约 ([\d.]+) GiB）合计至少需要 ([\d.]+) GiB（(\d+) MiB）可用空间，当前仅剩 ([\d.]+) GiB（(\d+) MiB）$/,
    format: (m, loc) => {
      const ttsLabel = translateInner(m[1]!, loc);
      const [, , ttsGib, nativeGib, combinedGib, combinedMib, freeGib, freeMib] = m;
      switch (loc) {
        case "en-US":
          return `Insufficient remote disk space: TTS engine ${ttsLabel} (~${ttsGib} GiB) and LongCat-Video-Avatar 1.6 native pipeline (~${nativeGib} GiB) require at least ${combinedGib} GiB (${combinedMib} MiB) free in total, but only ${freeGib} GiB (${freeMib} MiB) remains`;
        case "zh-TW":
          return `遠端磁碟空間不足：TTS 引擎 ${ttsLabel}（約 ${ttsGib} GiB）與 LongCat-Video-Avatar 1.6 原生管線（約 ${nativeGib} GiB）合計至少需要 ${combinedGib} GiB（${combinedMib} MiB）可用空間，目前僅剩 ${freeGib} GiB（${freeMib} MiB）`;
        case "zh-HK":
          return `遠端磁碟空間不足：TTS 引擎 ${ttsLabel}（約 ${ttsGib} GiB）與 LongCat-Video-Avatar 1.6 原生管線（約 ${nativeGib} GiB）合計至少需要 ${combinedGib} GiB（${combinedMib} MiB）可用空間，目前僅剩 ${freeGib} GiB（${freeMib} MiB）`;
        case "ja-JP":
          return `リモートのディスク空き容量不足: TTS エンジン ${ttsLabel}（約 ${ttsGib} GiB）と LongCat-Video-Avatar 1.6 ネイティブパイプライン（約 ${nativeGib} GiB）で合計少なくとも ${combinedGib} GiB（${combinedMib} MiB）の空き容量が必要ですが、残り ${freeGib} GiB（${freeMib} MiB）しかありません`;
        case "ko-KR":
          return `원격 디스크 공간 부족: TTS 엔진 ${ttsLabel}(약 ${ttsGib} GiB)과 LongCat-Video-Avatar 1.6 네이티브 파이프라인(약 ${nativeGib} GiB) 실행에 합계 최소 ${combinedGib} GiB(${combinedMib} MiB)의 여유 공간이 필요하지만 현재 ${freeGib} GiB(${freeMib} MiB)만 남아 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端磁盘空间不足：LongCat-Video-Avatar 1\.6 原生管线与所选采样\/人声模式至少需要 ([\d.]+) GiB（(\d+) MiB）可用空间，当前仅剩 ([\d.]+) GiB（(\d+) MiB）$/,
    format: (m, loc) => {
      const [, reqGib, reqMib, freeGib, freeMib] = m;
      switch (loc) {
        case "en-US":
          return `Insufficient remote disk space: LongCat-Video-Avatar 1.6 native pipeline with the selected sampling and vocal separation modes requires at least ${reqGib} GiB (${reqMib} MiB) free, but only ${freeGib} GiB (${freeMib} MiB) remains`;
        case "zh-TW":
          return `遠端磁碟空間不足：LongCat-Video-Avatar 1.6 原生管線與所選取樣/人聲模式至少需要 ${reqGib} GiB（${reqMib} MiB）可用空間，目前僅剩 ${freeGib} GiB（${freeMib} MiB）`;
        case "zh-HK":
          return `遠端磁碟空間不足：LongCat-Video-Avatar 1.6 原生管線與所選採樣/人聲模式至少需要 ${reqGib} GiB（${reqMib} MiB）可用空間，目前僅剩 ${freeGib} GiB（${freeMib} MiB）`;
        case "ja-JP":
          return `リモートのディスク空き容量不足: 選択したサンプリングおよびボーカル分離モードでの LongCat-Video-Avatar 1.6 ネイティブパイプラインには少なくとも ${reqGib} GiB（${reqMib} MiB）の空き容量が必要ですが、残り ${freeGib} GiB（${freeMib} MiB）しかありません`;
        case "ko-KR":
          return `원격 디스크 공간 부족: 선택한 샘플링 및 보컬 분리 모드의 LongCat-Video-Avatar 1.6 네이티브 파이프라인에는 최소 ${reqGib} GiB(${reqMib} MiB)의 여유 공간이 필요하지만 현재 ${freeGib} GiB(${freeMib} MiB)만 남아 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^配音完成后无法检测远端磁盘空间（(.+)），LongCat-Video-Avatar 1\.6 至少需要 ([\d.]+) GiB 可用磁盘空间$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      const reqGib = m[2];
      switch (loc) {
        case "en-US":
          return `Unable to check remote disk space after voice synthesis (${detail}); LongCat-Video-Avatar 1.6 requires at least ${reqGib} GiB of free disk space`;
        case "zh-TW":
          return `配音完成後無法偵測遠端磁碟空間（${detail}），LongCat-Video-Avatar 1.6 至少需要 ${reqGib} GiB 可用磁碟空間`;
        case "zh-HK":
          return `配音完成後無法檢測遠端磁碟空間（${detail}），LongCat-Video-Avatar 1.6 至少需要 ${reqGib} GiB 可用磁碟空間`;
        case "ja-JP":
          return `音声合成後にリモートのディスク空き容量を確認できませんでした（${detail}）。LongCat-Video-Avatar 1.6 には少なくとも ${reqGib} GiB の空きディスク容量が必要です`;
        case "ko-KR":
          return `음성 합성 후 원격 디스크 여유 공간을 확인할 수 없습니다 (${detail}). LongCat-Video-Avatar 1.6에는 최소 ${reqGib} GiB의 여유 디스크 공간이 필요합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端磁盘空间不足：配音完成后仅剩 ([\d.]+) GiB（(\d+) MiB），不足以继续运行 LongCat-Video-Avatar 1\.6 原生管线（至少需要 ([\d.]+) GiB \/ (\d+) MiB）$/,
    format: (m, loc) => {
      const [, freeGib, freeMib, reqGib, reqMib] = m;
      switch (loc) {
        case "en-US":
          return `Insufficient remote disk space: only ${freeGib} GiB (${freeMib} MiB) remains after voice synthesis, which is not enough to continue LongCat-Video-Avatar 1.6 native pipeline (at least ${reqGib} GiB / ${reqMib} MiB required)`;
        case "zh-TW":
          return `遠端磁碟空間不足：配音完成後僅剩 ${freeGib} GiB（${freeMib} MiB），不足以繼續執行 LongCat-Video-Avatar 1.6 原生管線（至少需要 ${reqGib} GiB / ${reqMib} MiB）`;
        case "zh-HK":
          return `遠端磁碟空間不足：配音完成後僅剩 ${freeGib} GiB（${freeMib} MiB），不足以繼續運行 LongCat-Video-Avatar 1.6 原生管線（至少需要 ${reqGib} GiB / ${reqMib} MiB）`;
        case "ja-JP":
          return `リモートのディスク空き容量不足: 音声合成後の残り容量は ${freeGib} GiB（${freeMib} MiB）のみで、LongCat-Video-Avatar 1.6 ネイティブパイプラインを続行できません（少なくとも ${reqGib} GiB / ${reqMib} MiB 必要）`;
        case "ko-KR":
          return `원격 디스크 공간 부족: 음성 합성 후 남은 공간이 ${freeGib} GiB(${freeMib} MiB)뿐이어서 LongCat-Video-Avatar 1.6 네이티브 파이프라인을 계속 실행할 수 없습니다 (최소 ${reqGib} GiB / ${reqMib} MiB 필요)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在导出作品到 Google 云盘 \((\d+)\/(\d+)\) · (.+)$/,
    format: (m, loc) => {
      const [, idx, total, fname] = m;
      switch (loc) {
        case "en-US":
          return `Exporting outputs to Google Drive (${idx}/${total}) · ${fname}`;
        case "zh-TW":
          return `正在匯出作品至 Google 雲端硬碟 (${idx}/${total}) · ${fname}`;
        case "zh-HK":
          return `正在匯出作品至 Google 雲端硬碟 (${idx}/${total}) · ${fname}`;
        case "ja-JP":
          return `Google ドライブへ作品を保存中 (${idx}/${total}) · ${fname}`;
        case "ko-KR":
          return `Google 드라이브로 결과물 내보내는 중 (${idx}/${total}) · ${fname}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^作品导出到 Google 云盘失败（(.+)），已保留当前实例$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to export output to Google Drive (${detail}); the instance was kept running`;
        case "zh-TW":
          return `作品匯出至 Google 雲端硬碟失敗（${detail}），已保留目前執行個體`;
        case "zh-HK":
          return `作品匯出至 Google 雲端硬碟失敗（${detail}），已保留目前實例`;
        case "ja-JP":
          return `Google ドライブへの出力保存に失敗しました（${detail}）。インスタンスを保持しました`;
        case "ko-KR":
          return `Google 드라이브로 결과물 내보내기 실패(${detail}). 인스턴스를 유지했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^工作区同步到 Google 云盘失败（(.+)），已保留当前实例$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Workspace sync to Google Drive failed (${detail}); the instance was kept running`;
        case "zh-TW":
          return `工作區同步至 Google 雲端硬碟失敗（${detail}），已保留目前執行個體`;
        case "zh-HK":
          return `工作區同步至 Google 雲端硬碟失敗（${detail}），已保留目前實例`;
        case "ja-JP":
          return `Google ドライブへのワークスペース同期に失敗しました（${detail}）。インスタンスを保持しました`;
        case "ko-KR":
          return `Google 드라이브 작업 공간 동기화 실패(${detail}). 인스턴스를 유지했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^读取 Google 云盘目录失败（HTTP (\d+)）$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Failed to inspect Google Drive folder (HTTP ${code})`;
        case "zh-TW":
          return `讀取 Google 雲端硬碟目錄失敗（HTTP ${code}）`;
        case "zh-HK":
          return `讀取 Google 雲端硬碟目錄失敗（HTTP ${code}）`;
        case "ja-JP":
          return `Google ドライブフォルダの読み取りに失敗しました（HTTP ${code}）`;
        case "ko-KR":
          return `Google 드라이브 폴더를 읽지 못했습니다 (HTTP ${code})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^读取 Google 云盘作品列表失败（HTTP (\d+)）$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Failed to list Google Drive outputs (HTTP ${code})`;
        case "zh-TW":
          return `讀取 Google 雲端硬碟作品清單失敗（HTTP ${code}）`;
        case "zh-HK":
          return `讀取 Google 雲端硬碟作品列表失敗（HTTP ${code}）`;
        case "ja-JP":
          return `Google ドライブ作品一覧の取得に失敗しました（HTTP ${code}）`;
        case "ko-KR":
          return `Google 드라이브 결과물 목록을 불러오지 못했습니다 (HTTP ${code})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^从 Google 云盘读取作品失败（HTTP (\d+)）$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Failed to read output from Google Drive (HTTP ${code})`;
        case "zh-TW":
          return `從 Google 雲端硬碟讀取作品失敗（HTTP ${code}）`;
        case "zh-HK":
          return `從 Google 雲端硬碟讀取作品失敗（HTTP ${code}）`;
        case "ja-JP":
          return `Google ドライブから作品を読み取れませんでした（HTTP ${code}）`;
        case "ko-KR":
          return `Google 드라이브에서 결과물을 읽지 못했습니다 (HTTP ${code})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^删除 Google 云盘作品失败（HTTP (\d+)）$/,
    format: (m, loc) => {
      const code = m[1];
      switch (loc) {
        case "en-US":
          return `Failed to delete output in Google Drive (HTTP ${code})`;
        case "zh-TW":
          return `刪除 Google 雲端硬碟作品失敗（HTTP ${code}）`;
        case "zh-HK":
          return `刪除 Google 雲端硬碟作品失敗（HTTP ${code}）`;
        case "ja-JP":
          return `Google ドライブ上の作品削除に失敗しました（HTTP ${code}）`;
        case "ko-KR":
          return `Google 드라이브의 결과물을 삭제하지 못했습니다 (HTTP ${code})`;
        default:
          return m[0];
      }
    },
  },
  // === Remote Web Console dynamic logs & errors ===
  {
    re: /^网页控制台已启动，监听 (.+)$/,
    format: (m, loc) => {
      const addr = m[1];
      switch (loc) {
        case "en-US":
          return `Web Console started on ${addr}`;
        case "zh-TW":
          return `網頁主控台已啟動，監聽 ${addr}`;
        case "zh-HK":
          return `網頁控制台已啟動，監聽 ${addr}`;
        case "ja-JP":
          return `Web コンソールを起動しました（待受: ${addr}）`;
        case "ko-KR":
          return `웹 콘솔이 시작되었습니다 (수신: ${addr})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^网页控制台已切换监听至 (.+)$/,
    format: (m, loc) => {
      const addr = m[1];
      switch (loc) {
        case "en-US":
          return `Web Console switched listener to ${addr}`;
        case "zh-TW":
          return `網頁主控台已切換監聽至 ${addr}`;
        case "zh-HK":
          return `網頁控制台已切換監聽至 ${addr}`;
        case "ja-JP":
          return `Web コンソールの待受先を ${addr} に切り替えました`;
        case "ko-KR":
          return `웹 콘솔 수신 주소를 ${addr}(으)로 전환했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^监听 ([^ ]+) 失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const addr = m[1];
      const detail = translateInner(m[2], loc);
      switch (loc) {
        case "en-US":
          return `Failed to listen on ${addr}: ${detail}`;
        case "zh-TW":
          return `監聽 ${addr} 失敗：${detail}`;
        case "zh-HK":
          return `監聽 ${addr} 失敗：${detail}`;
        case "ja-JP":
          return `${addr} の待受に失敗しました：${detail}`;
        case "ko-KR":
          return `${addr} 수신 시작 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^网页控制台启动失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to start Web Console: ${detail}`;
        case "zh-TW":
          return `網頁主控台啟動失敗：${detail}`;
        case "zh-HK":
          return `網頁控制台啟動失敗：${detail}`;
        case "ja-JP":
          return `Web コンソールの起動に失敗しました：${detail}`;
        case "ko-KR":
          return `웹 콘솔 시작 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^切换网页控制台端口失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to change Web Console port: ${detail}`;
        case "zh-TW":
          return `切換網頁主控台連接埠失敗：${detail}`;
        case "zh-HK":
          return `切換網頁控制台連接埠失敗：${detail}`;
        case "ja-JP":
          return `Web コンソールのポート変更に失敗しました：${detail}`;
        case "ko-KR":
          return `웹 콘솔 포트 변경 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^切换网页控制台监听模式失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to change Web Console listen scope: ${detail}`;
        case "zh-TW":
          return `切換網頁主控台監聽範圍失敗：${detail}`;
        case "zh-HK":
          return `切換網頁控制台監聽範圍失敗：${detail}`;
        case "ja-JP":
          return `Web コンソールの待受範囲変更に失敗しました：${detail}`;
        case "ko-KR":
          return `웹 콘솔 수신 범위 변경 실패: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^网页控制台服务异常退出[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Web Console server exited unexpectedly: ${detail}`;
        case "zh-TW":
          return `網頁主控台服務異常結束：${detail}`;
        case "zh-HK":
          return `網頁控制台服務異常退出：${detail}`;
        case "ja-JP":
          return `Web コンソールサーバーが予期せず終了しました：${detail}`;
        case "ko-KR":
          return `웹 콘솔 서버가 예기치 않게 종료되었습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^读取网页控制台访问口令失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to read Web Console access passcode: ${detail}`;
        case "zh-TW":
          return `讀取網頁主控台存取通行碼失敗：${detail}`;
        case "zh-HK":
          return `讀取網頁控制台存取通行碼失敗：${detail}`;
        case "ja-JP":
          return `Web コンソールのアクセスパスコード読み取りに失敗しました：${detail}`;
        case "ko-KR":
          return `웹 콘솔 접속 암호를 읽지 못했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^保存网页控制台访问口令失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to save Web Console access passcode: ${detail}`;
        case "zh-TW":
          return `儲存網頁主控台存取通行碼失敗：${detail}`;
        case "zh-HK":
          return `儲存網頁控制台存取通行碼失敗：${detail}`;
        case "ja-JP":
          return `Web コンソールのアクセスパスコード保存に失敗しました：${detail}`;
        case "ko-KR":
          return `웹 콘솔 접속 암호를 저장하지 못했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^保存网页控制台配置失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to save Web Console configuration: ${detail}`;
        case "zh-TW":
          return `儲存網頁主控台設定失敗：${detail}`;
        case "zh-HK":
          return `儲存網頁控制台設定失敗：${detail}`;
        case "ja-JP":
          return `Web コンソール設定の保存に失敗しました：${detail}`;
        case "ko-KR":
          return `웹 콘솔 설정을 저장하지 못했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^生成随机访问口令失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to generate random access passcode: ${detail}`;
        case "zh-TW":
          return `產生隨機存取通行碼失敗：${detail}`;
        case "zh-HK":
          return `產生隨機存取通行碼失敗：${detail}`;
        case "ja-JP":
          return `ランダムアクセスパスコードの生成に失敗しました：${detail}`;
        case "ko-KR":
          return `무작위 접속 암호를 생성하지 못했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^生成随机令牌失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1], loc);
      switch (loc) {
        case "en-US":
          return `Failed to generate random token: ${detail}`;
        case "zh-TW":
          return `產生隨機權杖失敗：${detail}`;
        case "zh-HK":
          return `產生隨機令牌失敗：${detail}`;
        case "ja-JP":
          return `ランダムトークンの生成に失敗しました：${detail}`;
        case "ko-KR":
          return `무작위 토큰을 생성하지 못했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },

  // === Live Studio dynamic activity logs, errors & validation rules ===
  {
    re: /^请求启动实时演播（视觉：(.+?) · 语音：(.+?) · 对话：(.+?) · 模式：(.+?)）$/,
    format: (m, loc) => {
      const [, vis, tts, llm, mode] = m;
      switch (loc) {
        case "en-US":
          return `Requesting Live Studio start (Visual: ${vis} · Voice: ${tts} · Chat: ${llm} · Mode: ${mode})`;
        case "zh-TW":
          return `請求啟動即時演播（視覺：${vis} · 語音：${tts} · 對話：${llm} · 模式：${mode}）`;
        case "zh-HK":
          return `請求啟動實時演播（視覺：${vis} · 語音：${tts} · 對話：${llm} · 模式：${mode}）`;
        case "ja-JP":
          return `ライブスタジオの起動をリクエストしています（映像: ${vis} ・ 音声: ${tts} ・ 対話: ${llm} ・ モード: ${mode}）`;
        case "ko-KR":
          return `라이브 스튜디오 시작 요청 중(비주얼: ${vis} · 음성: ${tts} · 대화: ${llm} · 모드: ${mode})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^远端已受理实时演播启动请求，但刷新状态失败（将自动重试）[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Remote service accepted Live Studio launch, but status refresh failed (will retry automatically): ${detail}`;
        case "zh-TW":
          return `遠端已受理即時演播啟動請求，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "zh-HK":
          return `遠端已受理實時演播啟動請求，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "ja-JP":
          return `リモートサービスがライブスタジオの起動リクエストを受理しましたが、状態更新に失敗しました（自動再試行します）: ${detail}`;
        case "ko-KR":
          return `원격 서비스가 라이브 스튜디오 시작 요청을 수락했지만 상태 새로고침에 실패했습니다(자동 재시도 예정): ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已停止远端实时演播服务，但刷新状态失败（将自动重试）[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Stopped remote Live Studio service, but status refresh failed (will retry automatically): ${detail}`;
        case "zh-TW":
          return `已停止遠端即時演播服務，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "zh-HK":
          return `已停止遠端實時演播服務，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "ja-JP":
          return `リモートライブスタジオサービスを停止しましたが、状態更新に失敗しました（自動再試行します）: ${detail}`;
        case "ko-KR":
          return `원격 라이브 스튜디오 서비스를 중지했지만 상태 새로고침에 실패했습니다(자동 재시도 예정): ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已保存实时演播外部 LLM 配置（模型：(.+?) · 密钥：(.+?)）$/,
    format: (m, loc) => {
      const model = m[1]!;
      const rawKey = m[2]!;
      const key =
        rawKey === "未设置"
          ? translateLiveStudioLabel("未设置", loc)
          : rawKey;
      switch (loc) {
        case "en-US":
          return `Saved Live Studio external LLM configuration (Model: ${model} · Key: ${key})`;
        case "zh-TW":
          return `已儲存即時演播外部 LLM 設定（模型：${model} · 金鑰：${key}）`;
        case "zh-HK":
          return `已儲存實時演播外部 LLM 配置（模型：${model} · 密鑰：${key}）`;
        case "ja-JP":
          return `ライブスタジオの外部 LLM 設定を保存しました（モデル: ${model} ・ キー: ${key}）`;
        case "ko-KR":
          return `라이브 스튜디오 외부 LLM 설정을 저장했습니다(모델: ${model} · 키: ${key})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^实时演播外部 LLM 连通性测试未通过（模型：(.+?)）$/,
    format: (m, loc) => {
      const model = m[1]!;
      switch (loc) {
        case "en-US":
          return `Live Studio external LLM connectivity test failed (Model: ${model})`;
        case "zh-TW":
          return `即時演播外部 LLM 連線測試未通過（模型：${model}）`;
        case "zh-HK":
          return `實時演播外部 LLM 連通性測試未通過（模型：${model}）`;
        case "ja-JP":
          return `ライブスタジオ外部 LLM の接続テストに失敗しました（モデル: ${model}）`;
        case "ko-KR":
          return `라이브 스튜디오 외부 LLM 연결 테스트에 실패했습니다(모델: ${model})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^实时演播外部 LLM 连通性测试通过（模型：(.+?)）$/,
    format: (m, loc) => {
      const model = m[1]!;
      switch (loc) {
        case "en-US":
          return `Live Studio external LLM connectivity test passed (Model: ${model})`;
        case "zh-TW":
          return `即時演播外部 LLM 連線測試通過（模型：${model}）`;
        case "zh-HK":
          return `實時演播外部 LLM 連通性測試通過（模型：${model}）`;
        case "ja-JP":
          return `ライブスタジオ外部 LLM の接続テストに成功しました（モデル: ${model}）`;
        case "ko-KR":
          return `라이브 스튜디오 외부 LLM 연결 테스트를 통과했습니다(모델: ${model})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(正在保存实时演播形象|实时演播形象已保存|正在删除实时演播形象|已删除实时演播形象|正在上传实时演播形象素材|实时演播形象素材上传完成|请求构建实时演播形象动作库|远端已受理形象动作库构建任务|正在上传实时演播驱动视频素材|实时演播驱动视频素材上传完成|正在删除实时演播驱动视频素材|已删除实时演播驱动视频素材)[：:]\s*(.+)$/,
    format: (m, loc) => {
      const actionMap: Record<string, ColabOneLocaleTable> = {
        正在保存实时演播形象: {
          "zh-CN": "正在保存实时演播形象",
          "en-US": "Saving Live Studio avatar",
          "zh-TW": "正在儲存即時演播形象",
          "zh-HK": "正在儲存實時演播形象",
          "ja-JP": "ライブスタジオのアバターを保存しています",
          "ko-KR": "라이브 스튜디오 아바타를 저장하는 중",
        },
        实时演播形象已保存: {
          "zh-CN": "实时演播形象已保存",
          "en-US": "Live Studio avatar saved",
          "zh-TW": "即時演播形象已儲存",
          "zh-HK": "實時演播形象已儲存",
          "ja-JP": "ライブスタジオのアバターを保存しました",
          "ko-KR": "라이브 스튜디오 아바타를 저장했습니다",
        },
        正在删除实时演播形象: {
          "zh-CN": "正在删除实时演播形象",
          "en-US": "Deleting Live Studio avatar",
          "zh-TW": "正在刪除即時演播形象",
          "zh-HK": "正在刪除實時演播形象",
          "ja-JP": "ライブスタジオのアバターを削除しています",
          "ko-KR": "라이브 스튜디오 아바타를 삭제하는 중",
        },
        已删除实时演播形象: {
          "zh-CN": "已删除实时演播形象",
          "en-US": "Deleted Live Studio avatar",
          "zh-TW": "已刪除即時演播形象",
          "zh-HK": "已刪除實時演播形象",
          "ja-JP": "ライブスタジオのアバターを削除しました",
          "ko-KR": "라이브 스튜디오 아바타를 삭제했습니다",
        },
        正在上传实时演播形象素材: {
          "zh-CN": "正在上传实时演播形象素材",
          "en-US": "Uploading Live Studio avatar asset",
          "zh-TW": "正在上傳即時演播形象素材",
          "zh-HK": "正在上傳實時演播形象素材",
          "ja-JP": "ライブスタジオのアバター素材をアップロードしています",
          "ko-KR": "라이브 스튜디오 아바타 에셋을 업로드하는 중",
        },
        实时演播形象素材上传完成: {
          "zh-CN": "实时演播形象素材上传完成",
          "en-US": "Live Studio avatar asset uploaded",
          "zh-TW": "即時演播形象素材上傳完成",
          "zh-HK": "實時演播形象素材上傳完成",
          "ja-JP": "ライブスタジオのアバター素材のアップロードが完了しました",
          "ko-KR": "라이브 스튜디오 아바타 에셋 업로드 완료",
        },
        请求构建实时演播形象动作库: {
          "zh-CN": "请求构建实时演播形象动作库",
          "en-US": "Requesting Live Studio avatar action pack build",
          "zh-TW": "請求建置即時演播形象動作庫",
          "zh-HK": "請求構建實時演播形象動作庫",
          "ja-JP": "ライブスタジオのアバターアクションパック構築をリクエストしています",
          "ko-KR": "라이브 스튜디오 아바타 동작 팩 빌드 요청 중",
        },
        远端已受理形象动作库构建任务: {
          "zh-CN": "远端已受理形象动作库构建任务",
          "en-US": "Remote service accepted avatar action pack build task",
          "zh-TW": "遠端已受理形象動作庫建置任務",
          "zh-HK": "遠端已受理形象動作庫構建任務",
          "ja-JP": "リモートサービスがアバターアクションパック構築タスクを受理しました",
          "ko-KR": "원격 서비스가 아바타 동작 팩 빌드 작업을 수락했습니다",
        },
        正在上传实时演播驱动视频素材: {
          "zh-CN": "正在上传实时演播驱动视频素材",
          "en-US": "Uploading Live Studio driving video asset",
          "zh-TW": "正在上傳即時演播驅動影片素材",
          "zh-HK": "正在上傳實時演播驅動影片素材",
          "ja-JP": "ライブスタジオの駆動動画素材をアップロードしています",
          "ko-KR": "라이브 스튜디오 구동 영상 에셋을 업로드하는 중",
        },
        实时演播驱动视频素材上传完成: {
          "zh-CN": "实时演播驱动视频素材上传完成",
          "en-US": "Live Studio driving video asset uploaded",
          "zh-TW": "即時演播驅動影片素材上傳完成",
          "zh-HK": "實時演播驅動影片素材上傳完成",
          "ja-JP": "ライブスタジオの駆動動画素材のアップロードが完了しました",
          "ko-KR": "라이브 스튜디오 구동 영상 에셋 업로드 완료",
        },
        正在删除实时演播驱动视频素材: {
          "zh-CN": "正在删除实时演播驱动视频素材",
          "en-US": "Deleting Live Studio driving video asset",
          "zh-TW": "正在刪除即時演播驅動影片素材",
          "zh-HK": "正在刪除實時演播驅動影片素材",
          "ja-JP": "ライブスタジオの駆動動画素材を削除しています",
          "ko-KR": "라이브 스튜디오 구동 영상 에셋을 삭제하는 중",
        },
        已删除实时演播驱动视频素材: {
          "zh-CN": "已删除实时演播驱动视频素材",
          "en-US": "Deleted Live Studio driving video asset",
          "zh-TW": "已刪除即時演播驅動影片素材",
          "zh-HK": "已刪除實時演播驅動影片素材",
          "ja-JP": "ライブスタジオの駆動動画素材を削除しました",
          "ko-KR": "라이브 스튜디오 구동 영상 에셋을 삭제했습니다",
        },
      };
      const prefix = actionMap[m[1]!]?.[loc] ?? m[1]!;
      const target = m[2]!;
      if (loc === "zh-TW" || loc === "zh-HK") {
        return `${prefix}：${target}`;
      }
      return `${prefix}: ${target}`;
    },
  },
  {
    re: /^(实时演播形象已保存|已删除实时演播形象|实时演播形象素材上传完成|远端已受理形象动作库构建任务|实时演播驱动视频素材上传完成|已删除实时演播驱动视频素材)，但刷新状态失败（将自动重试）[：:]\s*(.+)$/,
    format: (m, loc) => {
      const prefixMap: Record<string, ColabOneLocaleTable> = {
        实时演播形象已保存: {
          "zh-CN": "实时演播形象已保存",
          "en-US": "Live Studio avatar saved",
          "zh-TW": "即時演播形象已儲存",
          "zh-HK": "實時演播形象已儲存",
          "ja-JP": "ライブスタジオのアバターを保存しました",
          "ko-KR": "라이브 스튜디오 아바타를 저장했습니다",
        },
        已删除实时演播形象: {
          "zh-CN": "已删除实时演播形象",
          "en-US": "Deleted Live Studio avatar",
          "zh-TW": "已刪除即時演播形象",
          "zh-HK": "已刪除實時演播形象",
          "ja-JP": "ライブスタジオのアバターを削除しました",
          "ko-KR": "라이브 스튜디오 아바타를 삭제했습니다",
        },
        实时演播形象素材上传完成: {
          "zh-CN": "实时演播形象素材上传完成",
          "en-US": "Live Studio avatar asset uploaded",
          "zh-TW": "即時演播形象素材上傳完成",
          "zh-HK": "實時演播形象素材上傳完成",
          "ja-JP": "ライブスタジオのアバター素材のアップロードが完了しました",
          "ko-KR": "라이브 스튜디오 아바타 에셋을 업로드했습니다",
        },
        远端已受理形象动作库构建任务: {
          "zh-CN": "远端已受理形象动作库构建任务",
          "en-US": "Remote service accepted avatar action pack build task",
          "zh-TW": "遠端已受理形象動作庫建置任務",
          "zh-HK": "遠端已受理形象動作庫構建任務",
          "ja-JP": "リモートサービスがアバターアクションパック構築タスクを受理しました",
          "ko-KR": "원격 서비스가 아바타 동작 팩 빌드 작업을 수락했습니다",
        },
        实时演播驱动视频素材上传完成: {
          "zh-CN": "实时演播驱动视频素材上传完成",
          "en-US": "Live Studio driving video asset uploaded",
          "zh-TW": "即時演播驅動影片素材上傳完成",
          "zh-HK": "實時演播驅動影片素材上傳完成",
          "ja-JP": "ライブスタジオの駆動動画素材のアップロードが完了しました",
          "ko-KR": "라이브 스튜디오 구동 영상 에셋을 업로드했습니다",
        },
        已删除实时演播驱动视频素材: {
          "zh-CN": "已删除实时演播驱动视频素材",
          "en-US": "Deleted Live Studio driving video asset",
          "zh-TW": "已刪除即時演播驅動影片素材",
          "zh-HK": "已刪除實時演播驅動影片素材",
          "ja-JP": "ライブスタジオの駆動動画素材を削除しました",
          "ko-KR": "라이브 스튜디오 구동 영상 에셋을 삭제했습니다",
        },
      };
      const prefix = prefixMap[m[1]!]?.[loc] ?? m[1]!;
      const detail = translateInner(m[2]!, loc);
      switch (loc) {
        case "en-US":
          return `${prefix}, but status refresh failed (will retry automatically): ${detail}`;
        case "zh-TW":
          return `${prefix}，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "zh-HK":
          return `${prefix}，但重新整理狀態失敗（將自動重試）：${detail}`;
        case "ja-JP":
          return `${prefix}が、状態更新に失敗しました（自動再試行します）: ${detail}`;
        case "ko-KR":
          return `${prefix}. 단, 상태 새로고침에 실패했습니다(자동 재시도 예정): ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^Live Studio 本地服务 \(127\.0\.0\.1:(\d+)\) 启动(失败|超时)$/,
    format: (m, loc) => {
      const port = m[1]!;
      const isTimeout = m[2] === "超时";
      switch (loc) {
        case "en-US":
          return isTimeout
            ? `Live Studio local server (127.0.0.1:${port}) startup timed out`
            : `Live Studio local server (127.0.0.1:${port}) failed to start`;
        case "zh-TW":
          return isTimeout
            ? `Live Studio 本機服務 (127.0.0.1:${port}) 啟動逾時`
            : `Live Studio 本機服務 (127.0.0.1:${port}) 啟動失敗`;
        case "zh-HK":
          return isTimeout
            ? `Live Studio 本地服務 (127.0.0.1:${port}) 啟動逾時`
            : `Live Studio 本地服務 (127.0.0.1:${port}) 啟動失敗`;
        case "ja-JP":
          return isTimeout
            ? `Live Studio ローカルサーバー (127.0.0.1:${port}) の起動がタイムアウトしました`
            : `Live Studio ローカルサーバー (127.0.0.1:${port}) の起動に失敗しました`;
        case "ko-KR":
          return isTimeout
            ? `Live Studio 로컬 서버(127.0.0.1:${port}) 시작 시간이 초과되었습니다`
            : `Live Studio 로컬 서버(127.0.0.1:${port}) 시작에 실패했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^切换引擎显存预检未通过（需 (\d+) MiB，可用 (\d+) MiB）(?:。建议：(.+))?$/,
    format: (m, loc) => {
      const req = m[1]!;
      const avail = m[2]!;
      const rec = m[3] ? translateLiveStudioRecommendations(m[3], loc) : "";
      switch (loc) {
        case "en-US":
          return rec
            ? `Engine switch VRAM preflight failed (requires ${req} MiB, available ${avail} MiB). Suggestion: ${rec}`
            : `Engine switch VRAM preflight failed (requires ${req} MiB, available ${avail} MiB)`;
        case "zh-TW":
          return rec
            ? `切換引擎 VRAM 預檢未通過（需 ${req} MiB，可用 ${avail} MiB）。建議：${rec}`
            : `切換引擎 VRAM 預檢未通過（需 ${req} MiB，可用 ${avail} MiB）`;
        case "zh-HK":
          return rec
            ? `切換引擎顯存預檢未通過（需 ${req} MiB，可用 ${avail} MiB）。建議：${rec}`
            : `切換引擎顯存預檢未通過（需 ${req} MiB，可用 ${avail} MiB）`;
        case "ja-JP":
          return rec
            ? `エンジン切替の VRAM 事前チェックに失敗しました（必要 ${req} MiB、利用可能 ${avail} MiB）。推奨対応: ${rec}`
            : `エンジン切替の VRAM 事前チェックに失敗しました（必要 ${req} MiB、利用可能 ${avail} MiB）`;
        case "ko-KR":
          return rec
            ? `엔진 전환 VRAM 사전 점검에 실패했습니다(필요 ${req} MiB, 사용 가능 ${avail} MiB). 권장 조치: ${rec}`
            : `엔진 전환 VRAM 사전 점검에 실패했습니다(필요 ${req} MiB, 사용 가능 ${avail} MiB)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^显存预检未通过（需 (\d+) MiB，可用 (\d+) MiB）(?:。建议：(.+))?$/,
    format: (m, loc) => {
      const req = m[1]!;
      const avail = m[2]!;
      const rec = m[3] ? translateLiveStudioRecommendations(m[3], loc) : "";
      switch (loc) {
        case "en-US":
          return rec
            ? `VRAM preflight check failed (requires ${req} MiB, available ${avail} MiB). Suggestion: ${rec}`
            : `VRAM preflight check failed (requires ${req} MiB, available ${avail} MiB)`;
        case "zh-TW":
          return rec
            ? `VRAM 預檢未通過（需 ${req} MiB，可用 ${avail} MiB）。建議：${rec}`
            : `VRAM 預檢未通過（需 ${req} MiB，可用 ${avail} MiB）`;
        case "zh-HK":
          return rec
            ? `顯存預檢未通過（需 ${req} MiB，可用 ${avail} MiB）。建議：${rec}`
            : `顯存預檢未通過（需 ${req} MiB，可用 ${avail} MiB）`;
        case "ja-JP":
          return rec
            ? `VRAM 事前チェックに失敗しました（必要 ${req} MiB、利用可能 ${avail} MiB）。推奨対応: ${rec}`
            : `VRAM 事前チェックに失敗しました（必要 ${req} MiB、利用可能 ${avail} MiB）`;
        case "ko-KR":
          return rec
            ? `VRAM 사전 점검에 실패했습니다(필요 ${req} MiB, 사용 가능 ${avail} MiB). 권장 조치: ${rec}`
            : `VRAM 사전 점검에 실패했습니다(필요 ${req} MiB, 사용 가능 ${avail} MiB)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前远端已加载本地 LLM（约 (\d+) MiB），显存不足以与目标直播配置同驻（可用 (\d+) MiB）(?:。建议：(.+))?$/,
    format: (m, loc) => {
      const used = m[1]!;
      const avail = m[2]!;
      const rec = m[3] ? translateLiveStudioRecommendations(m[3], loc) : "";
      switch (loc) {
        case "en-US":
          return rec
            ? `A local LLM is currently loaded remotely (~${used} MiB); insufficient VRAM to co-reside with the target Live Studio configuration (${avail} MiB available). Suggestion: ${rec}`
            : `A local LLM is currently loaded remotely (~${used} MiB); insufficient VRAM to co-reside with the target Live Studio configuration (${avail} MiB available)`;
        case "zh-TW":
          return rec
            ? `目前遠端已載入本機 LLM（約 ${used} MiB），VRAM 不足以與目標直播設定共存（可用 ${avail} MiB）。建議：${rec}`
            : `目前遠端已載入本機 LLM（約 ${used} MiB），VRAM 不足以與目標直播設定共存（可用 ${avail} MiB）`;
        case "zh-HK":
          return rec
            ? `目前遠端已載入本地 LLM（約 ${used} MiB），顯存不足以與目標直播配置同駐（可用 ${avail} MiB）。建議：${rec}`
            : `目前遠端已載入本地 LLM（約 ${used} MiB），顯存不足以與目標直播配置同駐（可用 ${avail} MiB）`;
        case "ja-JP":
          return rec
            ? `リモートにローカル LLM（約 ${used} MiB）が読み込み済みのため、対象のライブ設定と同時常駐できる VRAM が不足しています（利用可能 ${avail} MiB）。推奨対応: ${rec}`
            : `リモートにローカル LLM（約 ${used} MiB）が読み込み済みのため、対象のライブ設定と同時常駐できる VRAM が不足しています（利用可能 ${avail} MiB）`;
        case "ko-KR":
          return rec
            ? `원격에 로컬 LLM(약 ${used} MiB)이 로드되어 있어 목표 라이브 구성과 동시 상주할 VRAM이 부족합니다(사용 가능 ${avail} MiB). 권장 조치: ${rec}`
            : `원격에 로컬 LLM(약 ${used} MiB)이 로드되어 있어 목표 라이브 구성과 동시 상주할 VRAM이 부족합니다(사용 가능 ${avail} MiB)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^当前远端已加载本地 LLM（约 (\d+) MiB）；使用外部 API 模式时建议先卸载本地模型以释放显存。$/,
    format: (m, loc) => {
      const used = m[1]!;
      switch (loc) {
        case "en-US":
          return `A local LLM is currently loaded remotely (~${used} MiB); unload it first to free VRAM when using external API mode.`;
        case "zh-TW":
          return `目前遠端已載入本機 LLM（約 ${used} MiB）；使用外部 API 模式時建議先卸載本機模型以釋放 VRAM。`;
        case "zh-HK":
          return `目前遠端已載入本地 LLM（約 ${used} MiB）；使用外部 API 模式時建議先卸載本地模型以釋放顯存。`;
        case "ja-JP":
          return `リモートにローカル LLM（約 ${used} MiB）が読み込み済みです。外部 API モードでは先にローカルモデルをアンロードして VRAM を解放することを推奨します。`;
        case "ko-KR":
          return `원격에 로컬 LLM(약 ${used} MiB)이 로드되어 있습니다. 외부 API 모드 사용 시 먼저 로컬 모델을 언로드하여 VRAM을 확보하는 것을 권장합니다.`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^实时直播所需显存约 (\d+) MiB，超过当前 GPU 可用显存 (\d+) MiB（缺口 (\d+) MiB）。$/,
    format: (m, loc) => {
      const req = m[1]!;
      const avail = m[2]!;
      const deficit = m[3]!;
      switch (loc) {
        case "en-US":
          return `Live streaming requires ~${req} MiB VRAM, exceeding available GPU VRAM of ${avail} MiB (short by ${deficit} MiB).`;
        case "zh-TW":
          return `即時直播所需 VRAM 約 ${req} MiB，超過目前 GPU 可用 VRAM ${avail} MiB（缺口 ${deficit} MiB）。`;
        case "zh-HK":
          return `實時直播所需顯存約 ${req} MiB，超過目前 GPU 可用顯存 ${avail} MiB（缺口 ${deficit} MiB）。`;
        case "ja-JP":
          return `ライブ配信に必要な VRAM は約 ${req} MiB で、現在の GPU 空き VRAM ${avail} MiB を超過しています（不足 ${deficit} MiB）。`;
        case "ko-KR":
          return `실시간 방송에 약 ${req} MiB VRAM이 필요하며 현재 사용 가능한 GPU VRAM ${avail} MiB를 초과합니다(${deficit} MiB 부족).`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^磁盘空间不足（剩余 (\d+) MiB，至少需要 (\d+) MiB）$/,
    format: (m, loc) => {
      const free = m[1]!;
      const need = m[2]!;
      switch (loc) {
        case "en-US":
          return `Insufficient disk space (${free} MiB free, at least ${need} MiB required)`;
        case "zh-TW":
          return `磁碟空間不足（剩餘 ${free} MiB，至少需要 ${need} MiB）`;
        case "zh-HK":
          return `磁碟空間不足（剩餘 ${free} MiB，至少需要 ${need} MiB）`;
        case "ja-JP":
          return `ディスク容量が不足しています（空き ${free} MiB、最低 ${need} MiB 必要）`;
        case "ko-KR":
          return `디스크 공간이 부족합니다(남은 공간 ${free} MiB, 최소 ${need} MiB 필요)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^外部 LLM (接口|对话探针)返回重定向 \((?:HTTP )?(\d+)\)，出于安全策略已拒绝跟随跳转$/,
    format: (m, loc) => {
      const isProbe = m[1] === "对话探针";
      const code = m[2]!;
      switch (loc) {
        case "en-US":
          return isProbe
            ? `External LLM chat probe returned a redirect (HTTP ${code}); following redirects is blocked by security policy`
            : `External LLM endpoint returned a redirect (HTTP ${code}); following redirects is blocked by security policy`;
        case "zh-TW":
          return isProbe
            ? `外部 LLM 對話探測傳回重新導向 (HTTP ${code})，基於安全原則已拒絕跟隨跳轉`
            : `外部 LLM 介面傳回重新導向 (HTTP ${code})，基於安全原則已拒絕跟隨跳轉`;
        case "zh-HK":
          return isProbe
            ? `外部 LLM 對話探測返回重定向 (HTTP ${code})，出於安全策略已拒絕跟隨跳轉`
            : `外部 LLM 介面返回重定向 (HTTP ${code})，出於安全策略已拒絕跟隨跳轉`;
        case "ja-JP":
          return isProbe
            ? `外部 LLM チャットプローブがリダイレクト (HTTP ${code}) を返しました。セキュリティポリシーによりリダイレクトの追跡を拒否しました`
            : `外部 LLM エンドポイントがリダイレクト (HTTP ${code}) を返しました。セキュリティポリシーによりリダイレクトの追跡を拒否しました`;
        case "ko-KR":
          return isProbe
            ? `외부 LLM 대화 프로브가 리디렉션(HTTP ${code})을 반환했으며 보안 정책에 따라 이동을 차단했습니다`
            : `외부 LLM 엔드포인트가 리디렉션(HTTP ${code})을 반환했으며 보안 정책에 따라 이동을 차단했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^接口返回状态码 (\d+)[：:]?\s*(.*)$/,
    format: (m, loc) => {
      const code = m[1]!;
      const detail = m[2] ? translateInner(m[2], loc) : "";
      switch (loc) {
        case "en-US":
          return detail
            ? `Endpoint returned status code ${code}: ${detail}`
            : `Endpoint returned status code ${code}`;
        case "zh-TW":
          return detail
            ? `介面傳回狀態碼 ${code}：${detail}`
            : `介面傳回狀態碼 ${code}`;
        case "zh-HK":
          return detail
            ? `介面返回狀態碼 ${code}：${detail}`
            : `介面返回狀態碼 ${code}`;
        case "ja-JP":
          return detail
            ? `エンドポイントがステータスコード ${code} を返しました: ${detail}`
            : `エンドポイントがステータスコード ${code} を返しました`;
        case "ko-KR":
          return detail
            ? `엔드포인트가 상태 코드 ${code}을(를) 반환했습니다: ${detail}`
            : `엔드포인트가 상태 코드 ${code}을(를) 반환했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^对话探针失败 \((\d+)\)[：:]?\s*(.*)$/,
    format: (m, loc) => {
      const code = m[1]!;
      const detail = m[2] ? translateInner(m[2], loc) : "";
      switch (loc) {
        case "en-US":
          return detail
            ? `Chat probe failed (HTTP ${code}): ${detail}`
            : `Chat probe failed (HTTP ${code})`;
        case "zh-TW":
          return detail
            ? `對話探測失敗 (${code})：${detail}`
            : `對話探測失敗 (${code})`;
        case "zh-HK":
          return detail
            ? `對話探測失敗 (${code})：${detail}`
            : `對話探測失敗 (${code})`;
        case "ja-JP":
          return detail
            ? `チャットプローブに失敗しました (${code}): ${detail}`
            : `チャットプローブに失敗しました (${code})`;
        case "ko-KR":
          return detail
            ? `대화 프로브 실패 (${code}): ${detail}`
            : `대화 프로브 실패 (${code})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^LLM 流式请求失败 \(HTTP (\d+)\)[：:]?\s*(.*)$/,
    format: (m, loc) => {
      const code = m[1]!;
      const detail = m[2] ? translateInner(m[2], loc) : "";
      switch (loc) {
        case "en-US":
          return detail
            ? `LLM streaming request failed (HTTP ${code}): ${detail}`
            : `LLM streaming request failed (HTTP ${code})`;
        case "zh-TW":
          return detail
            ? `LLM 串流請求失敗 (HTTP ${code})：${detail}`
            : `LLM 串流請求失敗 (HTTP ${code})`;
        case "zh-HK":
          return detail
            ? `LLM 串流請求失敗 (HTTP ${code})：${detail}`
            : `LLM 串流請求失敗 (HTTP ${code})`;
        case "ja-JP":
          return detail
            ? `LLM ストリーミングリクエストに失敗しました (HTTP ${code}): ${detail}`
            : `LLM ストリーミングリクエストに失敗しました (HTTP ${code})`;
        case "ko-KR":
          return detail
            ? `LLM 스트리밍 요청 실패 (HTTP ${code}): ${detail}`
            : `LLM 스트리밍 요청 실패 (HTTP ${code})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^LLM 流式连接异常[：:]\s*(.+)$/,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `LLM streaming connection error: ${detail}`;
        case "zh-TW":
          return `LLM 串流連線異常：${detail}`;
        case "zh-HK":
          return `LLM 串流連線異常：${detail}`;
        case "ja-JP":
          return `LLM ストリーミング接続エラー: ${detail}`;
        case "ko-KR":
          return `LLM 스트리밍 연결 오류: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^正在生成动作片段 \((\d+)\/(\d+)\)[：:]\s*(.+)$/,
    format: (m, loc) => {
      const [, idx, total, name] = m;
      switch (loc) {
        case "en-US":
          return `Generating action clip (${idx}/${total}): ${name}`;
        case "zh-TW":
          return `正在產生動作片段 (${idx}/${total})：${name}`;
        case "zh-HK":
          return `正在生成動作片段 (${idx}/${total})：${name}`;
        case "ja-JP":
          return `アクションクリップを生成中 (${idx}/${total}): ${name}`;
        case "ko-KR":
          return `동작 클립 생성 중 (${idx}/${total}): ${name}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^开始生成动作 (.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Starting action generation: ${id}`;
        case "zh-TW":
          return `開始產生動作 ${id}`;
        case "zh-HK":
          return `開始生成動作 ${id}`;
        case "ja-JP":
          return `アクション生成を開始: ${id}`;
        case "ko-KR":
          return `동작 생성 시작: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^生成的动作文件不存在或为符号链接[：:]\s*(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Generated action file does not exist or is a symbolic link: ${id}`;
        case "zh-TW":
          return `產生的動作檔案不存在或為符號連結：${id}`;
        case "zh-HK":
          return `生成的動作檔案不存在或為符號連結：${id}`;
        case "ja-JP":
          return `生成されたアクションファイルが存在しないかシンボリックリンクです: ${id}`;
        case "ko-KR":
          return `생성된 동작 파일이 없거나 심볼릭 링크입니다: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^动作构建器返回结果缺少 bytes 或 path[：:]\s*(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Action builder result is missing bytes or path: ${id}`;
        case "zh-TW":
          return `動作建置器傳回結果缺少 bytes 或 path：${id}`;
        case "zh-HK":
          return `動作構建器返回結果缺少 bytes 或 path：${id}`;
        case "ja-JP":
          return `アクションビルダーの結果に bytes または path がありません: ${id}`;
        case "ko-KR":
          return `동작 빌더 결과에 bytes 또는 path가 없습니다: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^动作构建器返回了不支持的结果类型[：:]\s*(.+)$/,
    format: (m, loc) => {
      const kind = m[1]!;
      switch (loc) {
        case "en-US":
          return `Action builder returned an unsupported result type: ${kind}`;
        case "zh-TW":
          return `動作建置器傳回了不支援的結果類型：${kind}`;
        case "zh-HK":
          return `動作構建器返回了不支援的結果類型：${kind}`;
        case "ja-JP":
          return `アクションビルダーが未対応の結果型を返しました: ${kind}`;
        case "ko-KR":
          return `동작 빌더가 지원하지 않는 결과 형식을 반환했습니다: ${kind}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^主播动作包构建完成（共 (\d+) 个动作）$/,
    format: (m, loc) => {
      const count = m[1]!;
      switch (loc) {
        case "en-US":
          return `Avatar action pack build complete (${count} actions)`;
        case "zh-TW":
          return `主播動作套件建置完成（共 ${count} 個動作）`;
        case "zh-HK":
          return `主播動作套件構建完成（共 ${count} 個動作）`;
        case "ja-JP":
          return `アバターアクションパックの構築が完了しました（全 ${count} アクション）`;
        case "ko-KR":
          return `아바타 동작 팩 빌드가 완료되었습니다(총 ${count}개 동작)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^主播动作包 (.+?) 构建完成$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Avatar action pack ${id} build complete`;
        case "zh-TW":
          return `主播動作套件 ${id} 建置完成`;
        case "zh-HK":
          return `主播動作套件 ${id} 構建完成`;
        case "ja-JP":
          return `アバターアクションパック ${id} の構築が完了しました`;
        case "ko-KR":
          return `아바타 동작 팩 ${id} 빌드가 완료되었습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(外部 LLM|RTMP 推流)\s*域名解析失败[：:]\s*(.+)$/,
    format: (m, loc) => {
      const isRtmp = m[1] === "RTMP 推流";
      const host = m[2]!;
      switch (loc) {
        case "en-US":
          return isRtmp
            ? `Failed to resolve RTMP stream hostname: ${host}`
            : `Failed to resolve external LLM hostname: ${host}`;
        case "zh-TW":
          return isRtmp
            ? `RTMP 串流推播網域解析失敗：${host}`
            : `外部 LLM 網域解析失敗：${host}`;
        case "zh-HK":
          return isRtmp
            ? `RTMP 推流網域解析失敗：${host}`
            : `外部 LLM 網域解析失敗：${host}`;
        case "ja-JP":
          return isRtmp
            ? `RTMP 配信ホスト名の解決に失敗しました: ${host}`
            : `外部 LLM ホスト名の解決に失敗しました: ${host}`;
        case "ko-KR":
          return isRtmp
            ? `RTMP 스트림 도메인 확인에 실패했습니다: ${host}`
            : `외부 LLM 도메인 확인에 실패했습니다: ${host}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(外部 LLM|RTMP 推流)\s*域名未解析到有效公网 IP[：:]\s*(.+)$/,
    format: (m, loc) => {
      const isRtmp = m[1] === "RTMP 推流";
      const host = m[2]!;
      switch (loc) {
        case "en-US":
          return isRtmp
            ? `RTMP stream hostname did not resolve to a valid public IP: ${host}`
            : `External LLM hostname did not resolve to a valid public IP: ${host}`;
        case "zh-TW":
          return isRtmp
            ? `RTMP 串流推播網域未解析到有效公用 IP：${host}`
            : `外部 LLM 網域未解析到有效公用 IP：${host}`;
        case "zh-HK":
          return isRtmp
            ? `RTMP 推流網域未解析到有效公網 IP：${host}`
            : `外部 LLM 網域未解析到有效公網 IP：${host}`;
        case "ja-JP":
          return isRtmp
            ? `RTMP 配信ホスト名が有効なパブリック IP に解決されませんでした: ${host}`
            : `外部 LLM ホスト名が有効なパブリック IP に解決されませんでした: ${host}`;
        case "ko-KR":
          return isRtmp
            ? `RTMP 스트림 도메인이 유효한 공인 IP로 확인되지 않았습니다: ${host}`
            : `외부 LLM 도메인이 유효한 공인 IP로 확인되지 않았습니다: ${host}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(外部 LLM 接口|推流地址)(主机名不合法|缺少有效主机名|禁止访问回环、内网或元数据域名|禁止访问非公网、私有、回环、CGNAT 或元数据 IP 地址|禁止使用非标准 IP 地址字面量)$/,
    format: (m, loc) => {
      const subj = translateLiveStudioLabel(m[1]!, loc);
      const reason = m[2]!;
      switch (reason) {
        case "主机名不合法":
          switch (loc) {
            case "en-US":
              return `${subj} hostname is invalid`;
            case "zh-TW":
              return `${subj}主機名稱無效`;
            case "zh-HK":
              return `${subj}主機名無效`;
            case "ja-JP":
              return `${subj}のホスト名が不正です`;
            case "ko-KR":
              return `${subj} 호스트 이름이 올바르지 않습니다`;
            default:
              return m[0];
          }
        case "缺少有效主机名":
          switch (loc) {
            case "en-US":
              return `${subj} is missing a valid hostname`;
            case "zh-TW":
              return `${subj}缺少有效主機名稱`;
            case "zh-HK":
              return `${subj}缺少有效主機名`;
            case "ja-JP":
              return `${subj}に有効なホスト名がありません`;
            case "ko-KR":
              return `${subj}에 유효한 호스트 이름이 없습니다`;
            default:
              return m[0];
          }
        case "禁止访问回环、内网或元数据域名":
          switch (loc) {
            case "en-US":
              return `${subj} cannot target loopback, internal, or metadata domains`;
            case "zh-TW":
              return `${subj}禁止存取迴路、內部網路或中繼資料網域`;
            case "zh-HK":
              return `${subj}禁止存取迴環、內網或元資料網域`;
            case "ja-JP":
              return `${subj}ではループバック・内部ネットワーク・メタデータドメインへのアクセスは禁止されています`;
            case "ko-KR":
              return `${subj}은(는) 루프백, 내부망 또는 메타데이터 도메인에 접근할 수 없습니다`;
            default:
              return m[0];
          }
        case "禁止访问非公网、私有、回环、CGNAT 或元数据 IP 地址":
          switch (loc) {
            case "en-US":
              return `${subj} cannot target non-public, private, loopback, CGNAT, or metadata IP addresses`;
            case "zh-TW":
              return `${subj}禁止存取非公用、私有、迴路、CGNAT 或中繼資料 IP 位址`;
            case "zh-HK":
              return `${subj}禁止存取非公網、私有、迴環、CGNAT 或元資料 IP 位址`;
            case "ja-JP":
              return `${subj}では非公開・プライベート・ループバック・CGNAT・メタデータ IP アドレスへのアクセスは禁止されています`;
            case "ko-KR":
              return `${subj}은(는) 비공인, 사설, 루프백, CGNAT 또는 메타데이터 IP 주소에 접근할 수 없습니다`;
            default:
              return m[0];
          }
        default:
          switch (loc) {
            case "en-US":
              return `${subj} does not allow non-standard IP address literals`;
            case "zh-TW":
              return `${subj}禁止使用非標準 IP 位址字面值`;
            case "zh-HK":
              return `${subj}禁止使用非標準 IP 位址字面值`;
            case "ja-JP":
              return `${subj}では非標準形式の IP アドレス表記は使用できません`;
            case "ko-KR":
              return `${subj}에서는 비표준 IP 주소 표기를 사용할 수 없습니다`;
            default:
              return m[0];
          }
      }
    },
  },
  {
    re: /^画布分辨率宽高必须在 (\d+) 到 (\d+) 之间$/,
    format: (m, loc) => {
      const [, lo, hi] = m;
      switch (loc) {
        case "en-US":
          return `Canvas resolution width and height must be between ${lo} and ${hi}`;
        case "zh-TW":
          return `畫布解析度寬高必須介於 ${lo} 到 ${hi} 之間`;
        case "zh-HK":
          return `畫布解像度寬高必須介於 ${lo} 到 ${hi} 之間`;
        case "ja-JP":
          return `キャンバス解像度の幅と高さは ${lo}〜${hi} の範囲で指定してください`;
        case "ko-KR":
          return `캔버스 해상도의 너비와 높이는 ${lo}에서 ${hi} 사이여야 합니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^二进制帧长度不足（至少 (\d+) 字节，实际 (\d+) 字节）$/,
    format: (m, loc) => {
      const [, minLen, actualLen] = m;
      switch (loc) {
        case "en-US":
          return `Binary frame is too short (expected at least ${minLen} bytes, got ${actualLen} bytes)`;
        case "zh-TW":
          return `二進位影格長度不足（至少 ${minLen} 位元組，實際 ${actualLen} 位元組）`;
        case "zh-HK":
          return `二進位幀長度不足（至少 ${minLen} 位元組，實際 ${actualLen} 位元組）`;
        case "ja-JP":
          return `バイナリフレーム長が不足しています（最小 ${minLen} バイト、実際 ${actualLen} バイト）`;
        case "ko-KR":
          return `바이너리 프레임 길이가 부족합니다(최소 ${minLen}바이트, 실제 ${actualLen}바이트)`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^未知的二进制帧类型[：:]\s*(.+)$/,
    format: (m, loc) => {
      const val = m[1]!;
      switch (loc) {
        case "en-US":
          return `Unknown binary frame type: ${val}`;
        case "zh-TW":
          return `未知的二進位影格類型：${val}`;
        case "zh-HK":
          return `未知的二進位幀類型：${val}`;
        case "ja-JP":
          return `不明なバイナリフレームタイプ: ${val}`;
        case "ko-KR":
          return `알 수 없는 바이너리 프레임 형식: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^非法的(二进制帧类型|epoch 值|pts_ms 值|模型版本标识|导播状态)[：:]\s*(.+)$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      const val = m[2]!;
      switch (loc) {
        case "en-US":
          return `Invalid ${label}: ${val}`;
        case "zh-TW":
          return `無效的${label}：${val}`;
        case "zh-HK":
          return `無效的${label}：${val}`;
        case "ja-JP":
          return `不正な${label}: ${val}`;
        case "ko-KR":
          return `올바르지 않은 ${label}: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^不支持的(视觉驱动引擎|语音合成引擎|对话模型模式|演播模式|麦克风模式|画布分辨率|动作类型|视觉引擎|流式 TTS 引擎|文件格式|LLM 模式|ffmpeg 编码器)[：:]\s*(.+)$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      const rawVal = m[2]!;
      const val =
        rawVal === "未知" ? translateLiveStudioLabel("未知", loc) : rawVal;
      switch (loc) {
        case "en-US":
          return `Unsupported ${label}: ${val}`;
        case "zh-TW":
          return `不支援的${label}：${val}`;
        case "zh-HK":
          return `不支援的${label}：${val}`;
        case "ja-JP":
          return `サポートされていない${label}: ${val}`;
        case "ko-KR":
          return `지원하지 않는 ${label}: ${val}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^驱动动作不存在[：:]\s*(.+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Driving motion does not exist: ${id}`;
        case "zh-TW":
          return `驅動動作不存在：${id}`;
        case "zh-HK":
          return `驅動動作不存在：${id}`;
        case "ja-JP":
          return `駆動モーションが存在しません: ${id}`;
        case "ko-KR":
          return `구동 모션이 존재하지 않습니다: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^目录字段 (.+?) 包含重复 ID$/,
    format: (m, loc) => {
      const field = m[1]!;
      switch (loc) {
        case "en-US":
          return `Catalog field ${field} contains duplicate IDs`;
        case "zh-TW":
          return `目錄欄位 ${field} 包含重複 ID`;
        case "zh-HK":
          return `目錄欄位 ${field} 包含重複 ID`;
        case "ja-JP":
          return `カタログフィールド ${field} に重複した ID が含まれています`;
        case "ko-KR":
          return `카탈로그 필드 ${field}에 중복된 ID가 포함되어 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(动作列表|商品列表)包含重复 ID[：:]\s*(.+)$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      const id = m[2]!;
      switch (loc) {
        case "en-US":
          return `${label} contains duplicate ID: ${id}`;
        case "zh-TW":
          return `${label}包含重複 ID：${id}`;
        case "zh-HK":
          return `${label}包含重複 ID：${id}`;
        case "ja-JP":
          return `${label}に重複した ID が含まれています: ${id}`;
        case "ko-KR":
          return `${label}에 중복된 ID가 포함되어 있습니다: ${id}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^trigger_keywords 数量超过上限 \((\d+)\)$/,
    format: (m, loc) => {
      const max = m[1]!;
      switch (loc) {
        case "en-US":
          return `trigger_keywords exceeds the maximum count (${max})`;
        case "zh-TW":
          return `trigger_keywords 數量超過上限 (${max})`;
        case "zh-HK":
          return `trigger_keywords 數量超過上限 (${max})`;
        case "ja-JP":
          return `trigger_keywords の件数が上限 (${max}) を超えています`;
        case "ko-KR":
          return `trigger_keywords 개수가 최대 한도(${max})를 초과했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^字段 (.+?) 数量超过上限 \((\d+)\)$/,
    format: (m, loc) => {
      const [, field, max] = m;
      switch (loc) {
        case "en-US":
          return `Field ${field} exceeds the maximum count (${max})`;
        case "zh-TW":
          return `欄位 ${field} 數量超過上限 (${max})`;
        case "zh-HK":
          return `欄位 ${field} 數量超過上限 (${max})`;
        case "ja-JP":
          return `フィールド ${field} の件数が上限 (${max}) を超えています`;
        case "ko-KR":
          return `${field} 필드의 개수가 최대 한도(${max})를 초과했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^字段 (.+?) (包含空元素|元素必须为对象|必须为对象数组|包含非字符串项|必须为字符串数组|包含非法标识)$/,
    format: (m, loc) => {
      const field = m[1]!;
      const kind = m[2]!;
      switch (kind) {
        case "包含空元素":
          switch (loc) {
            case "en-US":
              return `Field ${field} contains empty elements`;
            case "zh-TW":
              return `欄位 ${field} 包含空元素`;
            case "zh-HK":
              return `欄位 ${field} 包含空元素`;
            case "ja-JP":
              return `フィールド ${field} に空の要素が含まれています`;
            case "ko-KR":
              return `${field} 필드에 빈 요소가 포함되어 있습니다`;
            default:
              return m[0];
          }
        case "元素必须为对象":
          switch (loc) {
            case "en-US":
              return `Elements of field ${field} must be objects`;
            case "zh-TW":
              return `欄位 ${field} 元素必須為物件`;
            case "zh-HK":
              return `欄位 ${field} 元素必須為物件`;
            case "ja-JP":
              return `フィールド ${field} の要素はオブジェクトである必要があります`;
            case "ko-KR":
              return `${field} 필드의 요소는 객체여야 합니다`;
            default:
              return m[0];
          }
        case "必须为对象数组":
          switch (loc) {
            case "en-US":
              return `Field ${field} must be an array of objects`;
            case "zh-TW":
              return `欄位 ${field} 必須為物件陣列`;
            case "zh-HK":
              return `欄位 ${field} 必須為物件陣列`;
            case "ja-JP":
              return `フィールド ${field} はオブジェクト配列である必要があります`;
            case "ko-KR":
              return `${field} 필드는 객체 배열이어야 합니다`;
            default:
              return m[0];
          }
        case "包含非字符串项":
          switch (loc) {
            case "en-US":
              return `Field ${field} contains non-string items`;
            case "zh-TW":
              return `欄位 ${field} 包含非字串項目`;
            case "zh-HK":
              return `欄位 ${field} 包含非字串項目`;
            case "ja-JP":
              return `フィールド ${field} に文字列以外の項目が含まれています`;
            case "ko-KR":
              return `${field} 필드에 문자열이 아닌 항목이 포함되어 있습니다`;
            default:
              return m[0];
          }
        case "必须为字符串数组":
          switch (loc) {
            case "en-US":
              return `Field ${field} must be an array of strings`;
            case "zh-TW":
              return `欄位 ${field} 必須為字串陣列`;
            case "zh-HK":
              return `欄位 ${field} 必須為字串陣列`;
            case "ja-JP":
              return `フィールド ${field} は文字列配列である必要があります`;
            case "ko-KR":
              return `${field} 필드는 문자열 배열이어야 합니다`;
            default:
              return m[0];
          }
        default:
          switch (loc) {
            case "en-US":
              return `Field ${field} contains invalid identifiers`;
            case "zh-TW":
              return `欄位 ${field} 包含無效識別碼`;
            case "zh-HK":
              return `欄位 ${field} 包含無效識別碼`;
            case "ja-JP":
              return `フィールド ${field} に不正な識別子が含まれています`;
            case "ko-KR":
              return `${field} 필드에 잘못된 식별자가 포함되어 있습니다`;
            default:
              return m[0];
          }
      }
    },
  },
  {
    re: /^(.+?) 路径中不允许符号链接$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!, loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} path cannot contain symbolic links`;
        case "zh-TW":
          return `${label}${zhSep}路徑中不允許符號連結`;
        case "zh-HK":
          return `${label}${zhSep}路徑中不允許符號連結`;
        case "ja-JP":
          return `${label} のパスにシンボリックリンクは使用できません`;
        case "ko-KR":
          return `${label} 경로에는 심볼릭 링크를 사용할 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 路径越界$/,
    format: (m, loc) => {
      const label = translateSymlinkDirLabel(m[1]!, loc);
      const zhSep = /[A-Za-z0-9]$/.test(label) ? " " : "";
      switch (loc) {
        case "en-US":
          return `${label} path escapes the allowed directory`;
        case "zh-TW":
          return `${label}${zhSep}路徑超出允許範圍`;
        case "zh-HK":
          return `${label}${zhSep}路徑越界`;
        case "ja-JP":
          return `${label} のパスが許可された範囲外です`;
        case "ko-KR":
          return `${label} 경로가 허용 범위를 벗어났습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 文件名不能为空$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} filename cannot be empty`;
        case "zh-TW":
          return `${label}檔案名稱不可為空`;
        case "zh-HK":
          return `${label}檔案名稱不能為空`;
        case "ja-JP":
          return `${label}のファイル名を空にすることはできません`;
        case "ko-KR":
          return `${label} 파일 이름은 비워 둘 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 文件名不合法$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Invalid ${label} filename`;
        case "zh-TW":
          return `${label}檔案名稱無效`;
        case "zh-HK":
          return `${label}檔案名稱無效`;
        case "ja-JP":
          return `${label}のファイル名が不正です`;
        case "ko-KR":
          return `${label} 파일 이름이 올바르지 않습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 仅支持格式[：:]\s*(.+)$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      const formats = m[2]!;
      switch (loc) {
        case "en-US":
          return `${label} only supports formats: ${formats}`;
        case "zh-TW":
          return `${label}僅支援格式：${formats}`;
        case "zh-HK":
          return `${label}僅支援格式：${formats}`;
        case "ja-JP":
          return `${label}は次の形式のみ対応しています: ${formats}`;
        case "ko-KR":
          return `${label}은(는) 다음 형식만 지원합니다: ${formats}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) Base64 数据头不合法$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} has an invalid Base64 data header`;
        case "zh-TW":
          return `${label} Base64 資料標頭無效`;
        case "zh-HK":
          return `${label} Base64 資料標頭無效`;
        case "ja-JP":
          return `${label} の Base64 データヘッダーが不正です`;
        case "ko-KR":
          return `${label} Base64 데이터 헤더가 올바르지 않습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) Base64 编码无效$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} has invalid Base64 encoding`;
        case "zh-TW":
          return `${label} Base64 編碼無效`;
        case "zh-HK":
          return `${label} Base64 編碼無效`;
        case "ja-JP":
          return `${label} の Base64 エンコードが無効です`;
        case "ko-KR":
          return `${label} Base64 인코딩이 올바르지 않습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 数据为空$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} data is empty`;
        case "zh-TW":
          return `${label}資料為空`;
        case "zh-HK":
          return `${label}資料為空`;
        case "ja-JP":
          return `${label}のデータが空です`;
        case "ko-KR":
          return `${label} 데이터가 비어 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 超过大小上限$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} exceeds the maximum size limit`;
        case "zh-TW":
          return `${label}超過大小上限`;
        case "zh-HK":
          return `${label}超過大小上限`;
        case "ja-JP":
          return `${label}が最大サイズ上限を超えています`;
        case "ko-KR":
          return `${label}이(가) 최대 크기 제한을 초과했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*超过大小限制（最大 (\d+) MB）$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      const maxMb = m[2]!;
      switch (loc) {
        case "en-US":
          return `${label} exceeds the size limit (maximum ${maxMb} MB)`;
        case "zh-TW":
          return `${label}超過大小限制（最大 ${maxMb} MB）`;
        case "zh-HK":
          return `${label}超過大小限制（最大 ${maxMb} MB）`;
        case "ja-JP":
          return `${label}がサイズ上限を超えています（最大 ${maxMb} MB）`;
        case "ko-KR":
          return `${label}이(가) 크기 제한(최대 ${maxMb} MB)을 초과했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)内容为空$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} content is empty`;
        case "zh-TW":
          return `${label}內容為空`;
        case "zh-HK":
          return `${label}內容為空`;
        case "ja-JP":
          return `${label}の内容が空です`;
        case "ko-KR":
          return `${label} 내용이 비어 있습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)编码格式无效$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `${label} has an invalid encoding format`;
        case "zh-TW":
          return `${label}編碼格式無效`;
        case "zh-HK":
          return `${label}編碼格式無效`;
        case "ja-JP":
          return `${label}のエンコード形式が無効です`;
        case "ko-KR":
          return `${label} 인코딩 형식이 올바르지 않습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)包含不支持的字段[：:]\s*(.+)$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      const fields = m[2]!;
      switch (loc) {
        case "en-US":
          return `${label} contains unsupported fields: ${fields}`;
        case "zh-TW":
          return `${label}包含不支援的欄位：${fields}`;
        case "zh-HK":
          return `${label}包含不支援的欄位：${fields}`;
        case "ja-JP":
          return `${label}に未対応のフィールドが含まれています: ${fields}`;
        case "ko-KR":
          return `${label}에 지원하지 않는 필드가 포함되어 있습니다: ${fields}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 长度不能超过 (\d+)(?: 个字符)?$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      const max = m[2]!;
      switch (loc) {
        case "en-US":
          return `${label} length cannot exceed ${max} characters`;
        case "zh-TW":
          return `${label}長度不可超過 ${max} 個字元`;
        case "zh-HK":
          return `${label}長度不能超過 ${max} 個字元`;
        case "ja-JP":
          return `${label}の長さは ${max} 文字以内で指定してください`;
        case "ko-KR":
          return `${label} 길이는 ${max}자를 초과할 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 包含非法(空白或控制字符|控制字符|空字符|路径段)$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      const kind = m[2]!;
      switch (kind) {
        case "空白或控制字符":
          switch (loc) {
            case "en-US":
              return `${label} contains illegal whitespace or control characters`;
            case "zh-TW":
              return `${label}包含無效空白或控制字元`;
            case "zh-HK":
              return `${label}包含無效空白或控制字元`;
            case "ja-JP":
              return `${label}に不正な空白または制御文字が含まれています`;
            case "ko-KR":
              return `${label}에 잘못된 공백 또는 제어 문자가 포함되어 있습니다`;
            default:
              return m[0];
          }
        case "控制字符":
          switch (loc) {
            case "en-US":
              return `${label} contains illegal control characters`;
            case "zh-TW":
              return `${label}包含無效控制字元`;
            case "zh-HK":
              return `${label}包含無效控制字元`;
            case "ja-JP":
              return `${label}に不正な制御文字が含まれています`;
            case "ko-KR":
              return `${label}에 잘못된 제어 문자가 포함되어 있습니다`;
            default:
              return m[0];
          }
        case "空字符":
          switch (loc) {
            case "en-US":
              return `${label} contains illegal null characters`;
            case "zh-TW":
              return `${label}包含無效空字元`;
            case "zh-HK":
              return `${label}包含無效空字元`;
            case "ja-JP":
              return `${label}に不正なヌル文字が含まれています`;
            case "ko-KR":
              return `${label}에 잘못된 널 문자가 포함되어 있습니다`;
            default:
              return m[0];
          }
        default:
          switch (loc) {
            case "en-US":
              return `${label} contains illegal path segments`;
            case "zh-TW":
              return `${label}包含無效路徑區段`;
            case "zh-HK":
              return `${label}包含無效路徑段`;
            case "ja-JP":
              return `${label}に不正なパスセグメントが含まれています`;
            case "ko-KR":
              return `${label}에 잘못된 경로 세그먼트가 포함되어 있습니다`;
            default:
              return m[0];
          }
      }
    },
  },
  {
    re: /^请填写(.+)$/,
    when: (text) => Boolean(LIVE_STUDIO_LABELS[text.slice(3).trim()]),
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Please enter ${label}`;
        case "zh-TW":
          return `請填寫${label}`;
        case "zh-HK":
          return `請填寫${label}`;
        case "ja-JP":
          return `${label}を入力してください`;
        case "ko-KR":
          return `${label}을(를) 입력하세요`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 格式不合法$/,
    when: (text) =>
      Boolean(LIVE_STUDIO_LABELS[text.slice(0, -6).trim()]),
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Invalid ${label} format`;
        case "zh-TW":
          return `${label}格式無效`;
        case "zh-HK":
          return `${label}格式無效`;
        case "ja-JP":
          return `${label}の形式が不正です`;
        case "ko-KR":
          return `${label} 형식이 올바르지 않습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?) 不合法[：:]\s*(.+)$/,
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      const val = m[2]!;
      switch (loc) {
        case "en-US":
          return `Invalid ${label}: ${val}`;
        case "zh-TW":
          return `${label}無效：${val}`;
        case "zh-HK":
          return `${label}無效：${val}`;
        case "ja-JP":
          return `${label}が不正です: ${val}`;
        case "ko-KR":
          return `${label}이(가) 올바르지 않습니다: ${val}`;
        default:
          return m[0];
      }
    },
  },
  // === Workflow Web Sharing (console/internal/webconsole/workflow_share.go) ===
  {
    re: /^(读取|生成|保存)工作流分享签名密钥失败：(.+)$/s,
    format: (m, loc) => {
      const verb = m[1]!;
      const detail = translateInner(m[2]!, loc);
      switch (loc) {
        case "en-US": {
          const action =
            verb === "读取" ? "read" : verb === "生成" ? "generate" : "save";
          return `Failed to ${action} workflow share signing key: ${detail}`;
        }
        case "zh-TW": {
          const action =
            verb === "读取" ? "讀取" : verb === "生成" ? "產生" : "儲存";
          return `${action}工作流程分享簽章金鑰失敗：${detail}`;
        }
        case "zh-HK": {
          const action =
            verb === "读取" ? "讀取" : verb === "生成" ? "產生" : "儲存";
          return `${action}工作流分享簽章金鑰失敗：${detail}`;
        }
        case "ja-JP": {
          const action =
            verb === "读取" ? "読み込み" : verb === "生成" ? "生成" : "保存";
          return `ワークフロー共有の署名キーの${action}に失敗しました: ${detail}`;
        }
        case "ko-KR": {
          const action =
            verb === "读取"
              ? "읽지 못했습니다"
              : verb === "生成"
                ? "생성하지 못했습니다"
                : "저장하지 못했습니다";
          return `워크플로 공유 서명 키를 ${action}: ${detail}`;
        }
        default:
          return m[0];
      }
    },
  },
  {
    re: /^保存工作流分享配置失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to save workflow share configuration: ${detail}`;
        case "zh-TW":
          return `儲存工作流程分享設定失敗：${detail}`;
        case "zh-HK":
          return `儲存工作流分享配置失敗：${detail}`;
        case "ja-JP":
          return `ワークフロー共有設定の保存に失敗しました: ${detail}`;
        case "ko-KR":
          return `워크플로 공유 설정을 저장하지 못했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^生成分享链接编号失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to generate share link ID: ${detail}`;
        case "zh-TW":
          return `產生分享連結編號失敗：${detail}`;
        case "zh-HK":
          return `產生分享連結編號失敗：${detail}`;
        case "ja-JP":
          return `共有リンク ID の生成に失敗しました: ${detail}`;
        case "ko-KR":
          return `공유 링크 ID를 생성하지 못했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^生成分享链接随机凭证失败：(.+)$/s,
    format: (m, loc) => {
      const detail = translateInner(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Failed to generate random credentials for share link: ${detail}`;
        case "zh-TW":
          return `產生分享連結隨機憑證失敗：${detail}`;
        case "zh-HK":
          return `產生分享連結隨機憑證失敗：${detail}`;
        case "ja-JP":
          return `共有リンク用のランダム認証情報の生成に失敗しました: ${detail}`;
        case "ko-KR":
          return `공유 링크용 무작위 자격 증명을 생성하지 못했습니다: ${detail}`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(分享备注|页面标题)(包含无效字符|不能包含控制字符|长度不能超过 (\d+) 个字符)$/,
    format: (m, loc) => {
      const rawField = m[1]!;
      const rule = m[2]!;
      const limit = m[3];
      const fieldLabel =
        rawField === "分享备注"
          ? {
              "zh-CN": "分享备注",
              "en-US": "Share note",
              "zh-TW": "分享備註",
              "zh-HK": "分享備註",
              "ja-JP": "共有メモ",
              "ko-KR": "공유 메모",
            }[loc]
          : {
              "zh-CN": "页面标题",
              "en-US": "Page title",
              "zh-TW": "頁面標題",
              "zh-HK": "頁面標題",
              "ja-JP": "ページタイトル",
              "ko-KR": "페이지 제목",
            }[loc];
      if (rule === "包含无效字符") {
        switch (loc) {
          case "en-US":
            return `${fieldLabel} contains invalid characters`;
          case "zh-TW":
            return `${fieldLabel}包含無效字元`;
          case "zh-HK":
            return `${fieldLabel}包含無效字元`;
          case "ja-JP":
            return `${fieldLabel}に無効な文字が含まれています`;
          case "ko-KR":
            return `${fieldLabel}에 유효하지 않은 문자가 포함되어 있습니다`;
          default:
            return m[0];
        }
      }
      if (rule === "不能包含控制字符") {
        switch (loc) {
          case "en-US":
            return `${fieldLabel} cannot contain control characters`;
          case "zh-TW":
            return `${fieldLabel}不可包含控制字元`;
          case "zh-HK":
            return `${fieldLabel}不能包含控制字元`;
          case "ja-JP":
            return `${fieldLabel}に制御文字は使用できません`;
          case "ko-KR":
            return `${fieldLabel}에는 제어 문자를 포함할 수 없습니다`;
          default:
            return m[0];
        }
      }
      switch (loc) {
        case "en-US":
          return `${fieldLabel} cannot exceed ${limit} characters`;
        case "zh-TW":
          return `${fieldLabel}長度不可超過 ${limit} 個字元`;
        case "zh-HK":
          return `${fieldLabel}長度不能超過 ${limit} 個字元`;
        case "ja-JP":
          return `${fieldLabel}は ${limit} 文字以内で入力してください`;
        case "ko-KR":
          return `${fieldLabel} 길이는 ${limit}자를 초과할 수 없습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(清理|保存)(工作流分享|网页控制台)持久化会话失败：(.+)$/s,
    format: (m, loc) => {
      const verb = m[1]!;
      const rawScope = m[2]!;
      const detail = translateInner(m[3]!, loc);
      const isShare = rawScope === "工作流分享";
      switch (loc) {
        case "en-US": {
          const action = verb === "清理" ? "clean up" : "save";
          const scope = isShare ? "workflow share" : "Web Console";
          return `Failed to ${action} persisted ${scope} sessions: ${detail}`;
        }
        case "zh-TW": {
          const action = verb === "清理" ? "清理" : "儲存";
          const scope = isShare ? "工作流程分享" : "網頁主控台";
          return `${action}${scope}持久化工作階段失敗：${detail}`;
        }
        case "zh-HK": {
          const action = verb === "清理" ? "清理" : "儲存";
          const scope = isShare ? "工作流分享" : "網頁控制台";
          return `${action}${scope}持久化工作階段失敗：${detail}`;
        }
        case "ja-JP": {
          const action = verb === "清理" ? "クリーンアップ" : "保存";
          const scope = isShare ? "ワークフロー共有" : "Web コンソール";
          return `${scope}の永続セッションの${action}に失敗しました: ${detail}`;
        }
        case "ko-KR": {
          const action =
            verb === "清理" ? "정리하지 못했습니다" : "저장하지 못했습니다";
          const scope = isShare ? "워크플로 공유" : "웹 콘솔";
          return `${scope} 영구 세션을 ${action}: ${detail}`;
        }
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已创建工作流分享链接（(\d+) 个工作流，有效期 (once|24h|7d|forever)）$/,
    format: (m, loc) => {
      const count = m[1]!;
      const ttlKey = m[2] as "once" | "24h" | "7d" | "forever";
      const ttlLabel = {
        once: {
          "zh-CN": "once",
          "en-US": "single-use",
          "zh-TW": "一次性",
          "zh-HK": "一次性",
          "ja-JP": "1 回限り",
          "ko-KR": "1회용",
        },
        "24h": {
          "zh-CN": "24h",
          "en-US": "24 hours",
          "zh-TW": "24 小時",
          "zh-HK": "24 小時",
          "ja-JP": "24 時間",
          "ko-KR": "24시간",
        },
        "7d": {
          "zh-CN": "7d",
          "en-US": "7 days",
          "zh-TW": "7 天",
          "zh-HK": "7 天",
          "ja-JP": "7 日間",
          "ko-KR": "7일",
        },
        forever: {
          "zh-CN": "forever",
          "en-US": "never expires",
          "zh-TW": "長期有效",
          "zh-HK": "長期有效",
          "ja-JP": "無期限",
          "ko-KR": "무기한",
        },
      }[ttlKey][loc];
      switch (loc) {
        case "en-US":
          return `Created workflow share link (${count} ${count === "1" ? "workflow" : "workflows"}, validity: ${ttlLabel})`;
        case "zh-TW":
          return `已建立工作流程分享連結（${count} 個工作流程，有效期：${ttlLabel}）`;
        case "zh-HK":
          return `已建立工作流分享連結（${count} 個工作流，有效期：${ttlLabel}）`;
        case "ja-JP":
          return `ワークフロー共有リンクを作成しました（${count} 件のワークフロー・有効期限: ${ttlLabel}）`;
        case "ko-KR":
          return `워크플로 공유 링크를 생성했습니다 (${count}개 워크플로, 유효 기간: ${ttlLabel})`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已撤销工作流分享链接 ([0-9a-hjkmnp-tv-z]+) 并注销关联访客会话$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Revoked workflow share link ${id} and signed out linked guest sessions`;
        case "zh-TW":
          return `已撤銷工作流程分享連結 ${id} 並登出關聯訪客工作階段`;
        case "zh-HK":
          return `已撤銷工作流分享連結 ${id} 並登出關聯訪客工作階段`;
        case "ja-JP":
          return `ワークフロー共有リンク ${id} を取り消し、関連するゲストセッションをサインアウトしました`;
        case "ko-KR":
          return `워크플로 공유 링크 ${id}을(를) 철회하고 연결된 방문자 세션을 로그아웃했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^已删除工作流分享记录 ([0-9a-hjkmnp-tv-z]+)$/,
    format: (m, loc) => {
      const id = m[1]!;
      switch (loc) {
        case "en-US":
          return `Deleted workflow share record ${id}`;
        case "zh-TW":
          return `已刪除工作流程分享紀錄 ${id}`;
        case "zh-HK":
          return `已刪除工作流分享紀錄 ${id}`;
        case "ja-JP":
          return `ワークフロー共有履歴 ${id} を削除しました`;
        case "ko-KR":
          return `워크플로 공유 기록 ${id}을(를) 삭제했습니다`;
        default:
          return m[0];
      }
    },
  },
  {
    re: /^(.+?)\s*不合法$/,
    when: (text) =>
      Boolean(LIVE_STUDIO_LABELS[text.replace(/\s*不合法$/, "").trim()]),
    format: (m, loc) => {
      const label = translateLiveStudioLabel(m[1]!, loc);
      switch (loc) {
        case "en-US":
          return `Invalid ${label}`;
        case "zh-TW":
          return `${label}無效`;
        case "zh-HK":
          return `${label}無效`;
        case "ja-JP":
          return `${label}が不正です`;
        case "ko-KR":
          return `${label}이(가) 올바르지 않습니다`;
        default:
          return m[0];
      }
    },
  },
];

const LIVE_STUDIO_LABELS: Record<string, ColabOneLocaleTable> = {
  "外部 LLM 配置": {
    "zh-CN": "外部 LLM 配置",
    "en-US": "External LLM configuration",
    "zh-TW": "外部 LLM 設定",
    "zh-HK": "外部 LLM 配置",
    "ja-JP": "外部 LLM 設定",
    "ko-KR": "외부 LLM 설정",
  },
  实时演播配置: {
    "zh-CN": "实时演播配置",
    "en-US": "Live Studio configuration",
    "zh-TW": "即時演播設定",
    "zh-HK": "實時演播配置",
    "ja-JP": "ライブスタジオ設定",
    "ko-KR": "라이브 스튜디오 설정",
  },
  形象配置: {
    "zh-CN": "形象配置",
    "en-US": "Avatar configuration",
    "zh-TW": "形象設定",
    "zh-HK": "形象配置",
    "ja-JP": "アバター設定",
    "ko-KR": "아바타 설정",
  },
  动作配置: {
    "zh-CN": "动作配置",
    "en-US": "Action configuration",
    "zh-TW": "動作設定",
    "zh-HK": "動作配置",
    "ja-JP": "アクション設定",
    "ko-KR": "동작 설정",
  },
  商品配置: {
    "zh-CN": "商品配置",
    "en-US": "Product configuration",
    "zh-TW": "商品設定",
    "zh-HK": "商品配置",
    "ja-JP": "商品設定",
    "ko-KR": "상품 설정",
  },
  动作素材上传请求: {
    "zh-CN": "动作素材上传请求",
    "en-US": "Action asset upload request",
    "zh-TW": "動作素材上傳請求",
    "zh-HK": "動作素材上傳請求",
    "ja-JP": "アクション素材アップロードリクエスト",
    "ko-KR": "동작 에셋 업로드 요청",
  },
  驱动视频上传请求: {
    "zh-CN": "驱动视频上传请求",
    "en-US": "Driving video upload request",
    "zh-TW": "驅動影片上傳請求",
    "zh-HK": "驅動影片上傳請求",
    "ja-JP": "駆動動画アップロードリクエスト",
    "ko-KR": "구동 영상 업로드 요청",
  },
  形象动作库构建参数: {
    "zh-CN": "形象动作库构建参数",
    "en-US": "Avatar action pack build parameters",
    "zh-TW": "形象動作庫建置參數",
    "zh-HK": "形象動作庫構建參數",
    "ja-JP": "アバターアクションパック構築パラメータ",
    "ko-KR": "아바타 동작 팩 빌드 매개변수",
  },
  视觉驱动引擎: {
    "zh-CN": "视觉驱动引擎",
    "en-US": "visual driving engine",
    "zh-TW": "視覺驅動引擎",
    "zh-HK": "視覺驅動引擎",
    "ja-JP": "映像駆動エンジン",
    "ko-KR": "비주얼 구동 엔진",
  },
  视觉引擎: {
    "zh-CN": "视觉引擎",
    "en-US": "visual engine",
    "zh-TW": "視覺引擎",
    "zh-HK": "視覺引擎",
    "ja-JP": "映像エンジン",
    "ko-KR": "비주얼 엔진",
  },
  "视觉引擎 ID": {
    "zh-CN": "视觉引擎 ID",
    "en-US": "visual engine ID",
    "zh-TW": "視覺引擎 ID",
    "zh-HK": "視覺引擎 ID",
    "ja-JP": "映像エンジン ID",
    "ko-KR": "비주얼 엔진 ID",
  },
  语音合成引擎: {
    "zh-CN": "语音合成引擎",
    "en-US": "TTS engine",
    "zh-TW": "語音合成引擎",
    "zh-HK": "語音合成引擎",
    "ja-JP": "音声合成エンジン",
    "ko-KR": "음성 합성 엔진",
  },
  "流式 TTS 引擎": {
    "zh-CN": "流式 TTS 引擎",
    "en-US": "streaming TTS engine",
    "zh-TW": "串流 TTS 引擎",
    "zh-HK": "串流 TTS 引擎",
    "ja-JP": "ストリーミング TTS エンジン",
    "ko-KR": "스트리밍 TTS 엔진",
  },
  "流式 TTS 引擎 ID": {
    "zh-CN": "流式 TTS 引擎 ID",
    "en-US": "streaming TTS engine ID",
    "zh-TW": "串流 TTS 引擎 ID",
    "zh-HK": "串流 TTS 引擎 ID",
    "ja-JP": "ストリーミング TTS エンジン ID",
    "ko-KR": "스트리밍 TTS 엔진 ID",
  },
  "ASR/VAD 引擎 ID": {
    "zh-CN": "ASR/VAD 引擎 ID",
    "en-US": "ASR/VAD engine ID",
    "zh-TW": "ASR/VAD 引擎 ID",
    "zh-HK": "ASR/VAD 引擎 ID",
    "ja-JP": "ASR/VAD エンジン ID",
    "ko-KR": "ASR/VAD 엔진 ID",
  },
  音色预设: {
    "zh-CN": "音色预设",
    "en-US": "voice preset",
    "zh-TW": "音色預設",
    "zh-HK": "聲線預設",
    "ja-JP": "ボイスプリセット",
    "ko-KR": "음색 프리셋",
  },
  默认音色预设: {
    "zh-CN": "默认音色预设",
    "en-US": "default voice preset",
    "zh-TW": "預設音色",
    "zh-HK": "預設聲線",
    "ja-JP": "デフォルトボイスプリセット",
    "ko-KR": "기본 음색 프리셋",
  },
  对话模型模式: {
    "zh-CN": "对话模型模式",
    "en-US": "chat LLM mode",
    "zh-TW": "對話模型模式",
    "zh-HK": "對話模型模式",
    "ja-JP": "対話モデルモード",
    "ko-KR": "대화 모델 모드",
  },
  "LLM 模式": {
    "zh-CN": "LLM 模式",
    "en-US": "LLM mode",
    "zh-TW": "LLM 模式",
    "zh-HK": "LLM 模式",
    "ja-JP": "LLM モード",
    "ko-KR": "LLM 모드",
  },
  "ffmpeg 编码器": {
    "zh-CN": "ffmpeg 编码器",
    "en-US": "ffmpeg encoder",
    "zh-TW": "ffmpeg 編碼器",
    "zh-HK": "ffmpeg 編碼器",
    "ja-JP": "ffmpeg エンコーダ",
    "ko-KR": "ffmpeg 인코더",
  },
  "本地对话模型 ID": {
    "zh-CN": "本地对话模型 ID",
    "en-US": "local chat model ID",
    "zh-TW": "本機對話模型 ID",
    "zh-HK": "本地對話模型 ID",
    "ja-JP": "ローカル対話モデル ID",
    "ko-KR": "로컬 대화 모델 ID",
  },
  演播模式: {
    "zh-CN": "演播模式",
    "en-US": "director mode",
    "zh-TW": "演播模式",
    "zh-HK": "演播模式",
    "ja-JP": "スタジオモード",
    "ko-KR": "스튜디오 모드",
  },
  麦克风模式: {
    "zh-CN": "麦克风模式",
    "en-US": "microphone mode",
    "zh-TW": "麥克風模式",
    "zh-HK": "咪高峰模式",
    "ja-JP": "マイクモード",
    "ko-KR": "마이크 모드",
  },
  画布分辨率: {
    "zh-CN": "画布分辨率",
    "en-US": "canvas resolution",
    "zh-TW": "畫布解析度",
    "zh-HK": "畫布解像度",
    "ja-JP": "キャンバス解像度",
    "ko-KR": "캔버스 해상도",
  },
  "形象 ID": {
    "zh-CN": "形象 ID",
    "en-US": "avatar ID",
    "zh-TW": "形象 ID",
    "zh-HK": "形象 ID",
    "ja-JP": "アバター ID",
    "ko-KR": "아바타 ID",
  },
  "主播包 ID": {
    "zh-CN": "主播包 ID",
    "en-US": "avatar pack ID",
    "zh-TW": "主播套件 ID",
    "zh-HK": "主播套件 ID",
    "ja-JP": "アバターパック ID",
    "ko-KR": "아바타 팩 ID",
  },
  形象名称: {
    "zh-CN": "形象名称",
    "en-US": "avatar name",
    "zh-TW": "形象名稱",
    "zh-HK": "形象名稱",
    "ja-JP": "アバター名",
    "ko-KR": "아바타 이름",
  },
  人设提示词: {
    "zh-CN": "人设提示词",
    "en-US": "persona prompt",
    "zh-TW": "人設提示詞",
    "zh-HK": "人設提示詞",
    "ja-JP": "ペルソナプロンプト",
    "ko-KR": "페르소나 프롬프트",
  },
  "立绘素材 ID": {
    "zh-CN": "立绘素材 ID",
    "en-US": "portrait asset ID",
    "zh-TW": "立繪素材 ID",
    "zh-HK": "立繪素材 ID",
    "ja-JP": "ポートレート素材 ID",
    "ko-KR": "인물 에셋 ID",
  },
  立绘文件数据: {
    "zh-CN": "立绘文件数据",
    "en-US": "portrait file data",
    "zh-TW": "立繪檔案資料",
    "zh-HK": "立繪檔案資料",
    "ja-JP": "ポートレートファイルデータ",
    "ko-KR": "인물 파일 데이터",
  },
  立绘图片: {
    "zh-CN": "立绘图片",
    "en-US": "portrait image",
    "zh-TW": "立繪圖片",
    "zh-HK": "立繪圖片",
    "ja-JP": "ポートレート画像",
    "ko-KR": "인물 이미지",
  },
  立绘文件名: {
    "zh-CN": "立绘文件名",
    "en-US": "portrait filename",
    "zh-TW": "立繪檔案名稱",
    "zh-HK": "立繪檔案名稱",
    "ja-JP": "ポートレートファイル名",
    "ko-KR": "인물 파일 이름",
  },
  人像图片: {
    "zh-CN": "人像图片",
    "en-US": "Portrait image",
    "zh-TW": "人像圖片",
    "zh-HK": "人像圖片",
    "ja-JP": "ポートレート画像",
    "ko-KR": "인물 이미지",
  },
  人像路径: {
    "zh-CN": "人像路径",
    "en-US": "portrait path",
    "zh-TW": "人像路徑",
    "zh-HK": "人像路徑",
    "ja-JP": "ポートレートパス",
    "ko-KR": "인물 경로",
  },
  "动作 ID": {
    "zh-CN": "动作 ID",
    "en-US": "action ID",
    "zh-TW": "動作 ID",
    "zh-HK": "動作 ID",
    "ja-JP": "アクション ID",
    "ko-KR": "동작 ID",
  },
  "默认动作 ID": {
    "zh-CN": "默认动作 ID",
    "en-US": "default action ID",
    "zh-TW": "預設動作 ID",
    "zh-HK": "預設動作 ID",
    "ja-JP": "デフォルトアクション ID",
    "ko-KR": "기본 동작 ID",
  },
  动作名称: {
    "zh-CN": "动作名称",
    "en-US": "action name",
    "zh-TW": "動作名稱",
    "zh-HK": "動作名稱",
    "ja-JP": "アクション名",
    "ko-KR": "동작 이름",
  },
  动作类型: {
    "zh-CN": "动作类型",
    "en-US": "action type",
    "zh-TW": "動作類型",
    "zh-HK": "動作類型",
    "ja-JP": "アクションタイプ",
    "ko-KR": "동작 유형",
  },
  动作片段路径: {
    "zh-CN": "动作片段路径",
    "en-US": "action clip path",
    "zh-TW": "動作片段路徑",
    "zh-HK": "動作片段路徑",
    "ja-JP": "アクションクリップパス",
    "ko-KR": "동작 클립 경로",
  },
  动作视频: {
    "zh-CN": "动作视频",
    "en-US": "Action video",
    "zh-TW": "動作影片",
    "zh-HK": "動作影片",
    "ja-JP": "アクション動画",
    "ko-KR": "동작 영상",
  },
  背景音乐路径: {
    "zh-CN": "背景音乐路径",
    "en-US": "background music path",
    "zh-TW": "背景音樂路徑",
    "zh-HK": "背景音樂路徑",
    "ja-JP": "BGM パス",
    "ko-KR": "배경 음악 경로",
  },
  背景音乐: {
    "zh-CN": "背景音乐",
    "en-US": "Background music",
    "zh-TW": "背景音樂",
    "zh-HK": "背景音樂",
    "ja-JP": "BGM",
    "ko-KR": "배경 음악",
  },
  背景音乐文件名: {
    "zh-CN": "背景音乐文件名",
    "en-US": "background music filename",
    "zh-TW": "背景音樂檔案名稱",
    "zh-HK": "背景音樂檔案名稱",
    "ja-JP": "BGM ファイル名",
    "ko-KR": "배경 음악 파일 이름",
  },
  背景音乐数据: {
    "zh-CN": "背景音乐数据",
    "en-US": "background music data",
    "zh-TW": "背景音樂資料",
    "zh-HK": "背景音樂資料",
    "ja-JP": "BGM データ",
    "ko-KR": "배경 음악 데이터",
  },
  背景音乐文件: {
    "zh-CN": "背景音乐文件",
    "en-US": "background music file",
    "zh-TW": "背景音樂檔案",
    "zh-HK": "背景音樂檔案",
    "ja-JP": "BGM ファイル",
    "ko-KR": "배경 음악 파일",
  },
  驱动视频: {
    "zh-CN": "驱动视频",
    "en-US": "Driving video",
    "zh-TW": "驅動影片",
    "zh-HK": "驅動影片",
    "ja-JP": "駆動動画",
    "ko-KR": "구동 영상",
  },
  驱动背景音乐: {
    "zh-CN": "驱动背景音乐",
    "en-US": "Driving background music",
    "zh-TW": "驅動背景音樂",
    "zh-HK": "驅動背景音樂",
    "ja-JP": "駆動 BGM",
    "ko-KR": "구동 배경 음악",
  },
  驱动动作名称: {
    "zh-CN": "驱动动作名称",
    "en-US": "driving motion name",
    "zh-TW": "驅動動作名稱",
    "zh-HK": "驅動動作名稱",
    "ja-JP": "駆動モーション名",
    "ko-KR": "구동 모션 이름",
  },
  "驱动动作 ID": {
    "zh-CN": "驱动动作 ID",
    "en-US": "driving motion ID",
    "zh-TW": "驅動動作 ID",
    "zh-HK": "驅動動作 ID",
    "ja-JP": "駆動モーション ID",
    "ko-KR": "구동 모션 ID",
  },
  "商品 ID": {
    "zh-CN": "商品 ID",
    "en-US": "product ID",
    "zh-TW": "商品 ID",
    "zh-HK": "商品 ID",
    "ja-JP": "商品 ID",
    "ko-KR": "상품 ID",
  },
  商品名称: {
    "zh-CN": "商品名称",
    "en-US": "product name",
    "zh-TW": "商品名稱",
    "zh-HK": "商品名稱",
    "ja-JP": "商品名",
    "ko-KR": "상품 이름",
  },
  商品价格: {
    "zh-CN": "商品价格",
    "en-US": "product price",
    "zh-TW": "商品價格",
    "zh-HK": "商品價格",
    "ja-JP": "商品価格",
    "ko-KR": "상품 가격",
  },
  商品标签: {
    "zh-CN": "商品标签",
    "en-US": "product tag",
    "zh-TW": "商品標籤",
    "zh-HK": "商品標籤",
    "ja-JP": "商品タグ",
    "ko-KR": "상품 태그",
  },
  商品卖点: {
    "zh-CN": "商品卖点",
    "en-US": "product highlights",
    "zh-TW": "商品賣點",
    "zh-HK": "商品賣點",
    "ja-JP": "商品の特徴",
    "ko-KR": "상품 특징",
  },
  话术提示: {
    "zh-CN": "话术提示",
    "en-US": "script hint",
    "zh-TW": "話術提示",
    "zh-HK": "話術提示",
    "ja-JP": "トークヒント",
    "ko-KR": "멘트 힌트",
  },
  "商品图片 ID": {
    "zh-CN": "商品图片 ID",
    "en-US": "product image ID",
    "zh-TW": "商品圖片 ID",
    "zh-HK": "商品圖片 ID",
    "ja-JP": "商品画像 ID",
    "ko-KR": "상품 이미지 ID",
  },
  触发关键词: {
    "zh-CN": "触发关键词",
    "en-US": "trigger keyword",
    "zh-TW": "觸發關鍵字",
    "zh-HK": "觸發關鍵詞",
    "ja-JP": "トリガーキーワード",
    "ko-KR": "트리거 키워드",
  },
  素材文件名: {
    "zh-CN": "素材文件名",
    "en-US": "asset filename",
    "zh-TW": "素材檔案名稱",
    "zh-HK": "素材檔案名稱",
    "ja-JP": "素材ファイル名",
    "ko-KR": "에셋 파일 이름",
  },
  素材数据: {
    "zh-CN": "素材数据",
    "en-US": "asset data",
    "zh-TW": "素材資料",
    "zh-HK": "素材資料",
    "ja-JP": "素材データ",
    "ko-KR": "에셋 데이터",
  },
  素材文件: {
    "zh-CN": "素材文件",
    "en-US": "asset file",
    "zh-TW": "素材檔案",
    "zh-HK": "素材檔案",
    "ja-JP": "素材ファイル",
    "ko-KR": "에셋 파일",
  },
  "外部 LLM 地址": {
    "zh-CN": "外部 LLM 地址",
    "en-US": "external LLM URL",
    "zh-TW": "外部 LLM 位址",
    "zh-HK": "外部 LLM 位址",
    "ja-JP": "外部 LLM URL",
    "ko-KR": "외부 LLM URL",
  },
  "外部 LLM 接口": {
    "zh-CN": "外部 LLM 接口",
    "en-US": "External LLM endpoint",
    "zh-TW": "外部 LLM 介面",
    "zh-HK": "外部 LLM 介面",
    "ja-JP": "外部 LLM エンドポイント",
    "ko-KR": "외부 LLM 엔드포인트",
  },
  "外部 LLM 模型名称": {
    "zh-CN": "外部 LLM 模型名称",
    "en-US": "external LLM model name",
    "zh-TW": "外部 LLM 模型名稱",
    "zh-HK": "外部 LLM 模型名稱",
    "ja-JP": "外部 LLM モデル名",
    "ko-KR": "외부 LLM 모델 이름",
  },
  "RTMP 推流地址": {
    "zh-CN": "RTMP 推流地址",
    "en-US": "RTMP stream URL",
    "zh-TW": "RTMP 串流推播位址",
    "zh-HK": "RTMP 推流位址",
    "ja-JP": "RTMP 配信 URL",
    "ko-KR": "RTMP 스트림 URL",
  },
  推流地址: {
    "zh-CN": "推流地址",
    "en-US": "Stream URL",
    "zh-TW": "串流推播位址",
    "zh-HK": "推流位址",
    "ja-JP": "配信 URL",
    "ko-KR": "스트림 URL",
  },
  文件格式: {
    "zh-CN": "文件格式",
    "en-US": "file format",
    "zh-TW": "檔案格式",
    "zh-HK": "檔案格式",
    "ja-JP": "ファイル形式",
    "ko-KR": "파일 형식",
  },
  动作列表: {
    "zh-CN": "动作列表",
    "en-US": "Action list",
    "zh-TW": "動作清單",
    "zh-HK": "動作清單",
    "ja-JP": "アクション一覧",
    "ko-KR": "동작 목록",
  },
  商品列表: {
    "zh-CN": "商品列表",
    "en-US": "Product list",
    "zh-TW": "商品清單",
    "zh-HK": "商品清單",
    "ja-JP": "商品一覧",
    "ko-KR": "상품 목록",
  },
  二进制帧类型: {
    "zh-CN": "二进制帧类型",
    "en-US": "binary frame type",
    "zh-TW": "二進位影格類型",
    "zh-HK": "二進位幀類型",
    "ja-JP": "バイナリフレームタイプ",
    "ko-KR": "바이너리 프레임 형식",
  },
  "epoch 值": {
    "zh-CN": "epoch 值",
    "en-US": "epoch value",
    "zh-TW": "epoch 值",
    "zh-HK": "epoch 值",
    "ja-JP": "epoch 値",
    "ko-KR": "epoch 값",
  },
  "pts_ms 值": {
    "zh-CN": "pts_ms 值",
    "en-US": "pts_ms value",
    "zh-TW": "pts_ms 值",
    "zh-HK": "pts_ms 值",
    "ja-JP": "pts_ms 値",
    "ko-KR": "pts_ms 값",
  },
  模型版本标识: {
    "zh-CN": "模型版本标识",
    "en-US": "model revision identifier",
    "zh-TW": "模型版本識別碼",
    "zh-HK": "模型版本識別碼",
    "ja-JP": "モデルリビジョン識別子",
    "ko-KR": "모델 리비전 식별자",
  },
  导播状态: {
    "zh-CN": "导播状态",
    "en-US": "director state",
    "zh-TW": "導播狀態",
    "zh-HK": "導播狀態",
    "ja-JP": "ディレクター状態",
    "ko-KR": "디렉터 상태",
  },
  路径: {
    "zh-CN": "路径",
    "en-US": "path",
    "zh-TW": "路徑",
    "zh-HK": "路徑",
    "ja-JP": "パス",
    "ko-KR": "경로",
  },
  未设置: {
    "zh-CN": "未设置",
    "en-US": "not set",
    "zh-TW": "未設定",
    "zh-HK": "未設定",
    "ja-JP": "未設定",
    "ko-KR": "미설정",
  },
  未知: {
    "zh-CN": "未知",
    "en-US": "unknown",
    "zh-TW": "未知",
    "zh-HK": "未知",
    "ja-JP": "不明",
    "ko-KR": "알 수 없음",
  },
};

function translateLiveStudioLabel(raw: string, loc: ColabOneLocale): string {
  const key = raw.trim();
  const hit = LIVE_STUDIO_LABELS[key] ?? SYMLINK_DIR_LABELS[key];
  return hit ? hit[loc] : key;
}

function translateLiveStudioRecommendations(
  raw: string,
  loc: ColabOneLocale,
): string {
  const parts = raw
    .split("；")
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => translateInner(part, loc));
  if (parts.length === 0) return raw;
  if (loc === "en-US" || loc === "ko-KR") {
    return parts.join(" ");
  }
  if (loc === "ja-JP") {
    return parts.join("");
  }
  return parts.join("；");
}

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
