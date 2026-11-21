import type { ColabOneLocale } from "./flags";

type LocaleTable = Record<ColabOneLocale, string>;

const SOURCE_LABELS: Record<string, LocaleTable> = {
  客户端: {
    "zh-CN": "客户端",
    "en-US": "Client",
    "zh-TW": "用戶端",
    "zh-HK": "程式",
    "ja-JP": "クライアント",
    "ko-KR": "클라이언트",
  },
  远端: {
    "zh-CN": "远端",
    "en-US": "Remote",
    "zh-TW": "遠端",
    "zh-HK": "遠端",
    "ja-JP": "リモート",
    "ko-KR": "원격",
  },
  "远端 GPU": {
    "zh-CN": "远端 GPU",
    "en-US": "Remote GPU",
    "zh-TW": "遠端 GPU",
    "zh-HK": "遠端 GPU",
    "ja-JP": "リモート GPU",
    "ko-KR": "원격 GPU",
  },
  "对外 API": {
    "zh-CN": "对外 API",
    "en-US": "Public API",
    "zh-TW": "對外 API",
    "zh-HK": "對外 API",
    "ja-JP": "外部 API",
    "ko-KR": "외부 API",
  },
  登录: {
    "zh-CN": "登录",
    "en-US": "Auth",
    "zh-TW": "登入",
    "zh-HK": "登入",
    "ja-JP": "ログイン",
    "ko-KR": "로그인",
  },
  计费: {
    "zh-CN": "计费",
    "en-US": "Billing",
    "zh-TW": "計費",
    "zh-HK": "計費",
    "ja-JP": "課金",
    "ko-KR": "과금",
  },
  模型: {
    "zh-CN": "模型",
    "en-US": "Model",
    "zh-TW": "模型",
    "zh-HK": "模型",
    "ja-JP": "モデル",
    "ko-KR": "모델",
  },
  设置: {
    "zh-CN": "设置",
    "en-US": "Settings",
    "zh-TW": "設定",
    "zh-HK": "設定",
    "ja-JP": "設定",
    "ko-KR": "설정",
  },
  云盘: {
    "zh-CN": "云盘",
    "en-US": "Drive",
    "zh-TW": "雲端硬碟",
    "zh-HK": "雲端硬碟",
    "ja-JP": "ドライブ",
    "ko-KR": "드라이브",
  },
  工作流: {
    "zh-CN": "工作流",
    "en-US": "Workflow",
    "zh-TW": "工作流程",
    "zh-HK": "工作流",
    "ja-JP": "ワークフロー",
    "ko-KR": "워크플로",
  },
  监控: {
    "zh-CN": "监控",
    "en-US": "Monitor",
    "zh-TW": "監控",
    "zh-HK": "監控",
    "ja-JP": "監視",
    "ko-KR": "모니터",
  },
};

const EXACT_MESSAGES: Record<string, LocaleTable> = {
  客户端已启动: {
    "zh-CN": "客户端已启动",
    "en-US": "ColabOne client started",
    "zh-TW": "用戶端已啟動",
    "zh-HK": "程式已啟動",
    "ja-JP": "クライアントを起動しました",
    "ko-KR": "클라이언트가 시작되었습니다",
  },
  "已恢复本机保存的 Google 登录；到期自动续期": {
    "zh-CN": "已恢复本机保存的 Google 登录；到期自动续期",
    "en-US": "Restored saved Google login; tokens renew automatically",
    "zh-TW": "已復原本機儲存的 Google 登入；到期將自動續期",
    "zh-HK": "已恢復本地儲存的 Google 登入；到期自動續期",
    "ja-JP": "保存済みの Google ログインを復元しました（期限時に自動更新）",
    "ko-KR": "저장된 Google 로그인을 복원했습니다 (만료 시 자동 갱신)",
  },
  "正在刷新 Google 账号余额与运行实例状态": {
    "zh-CN": "正在刷新 Google 账号余额与运行实例状态",
    "en-US": "Refreshing Google account balance and running instances",
    "zh-TW": "正在重新整理 Google 帳戶餘額與執行中個體狀態",
    "zh-HK": "正在重新整理 Google 帳戶餘額與運行實例狀態",
    "ja-JP": "Google アカウントの残高と稼働中インスタンスを更新しています",
    "ko-KR": "Google 계정 잔액 및 실행 중인 인스턴스 상태를 새로고침하는 중",
  },
  "账号状态已刷新（当前未登录）": {
    "zh-CN": "账号状态已刷新（当前未登录）",
    "en-US": "Account status refreshed (signed out)",
    "zh-TW": "帳戶狀態已重新整理（尚未登入）",
    "zh-HK": "帳戶狀態已重新整理（目前未登入）",
    "ja-JP": "アカウント状態を更新しました（未ログイン）",
    "ko-KR": "계정 상태를 새로고침했습니다 (로그아웃 상태)",
  },
  "远端 GPU 已连接": {
    "zh-CN": "远端 GPU 已连接",
    "en-US": "Remote GPU connected",
    "zh-TW": "遠端 GPU 已連線",
    "zh-HK": "遠端 GPU 已連線",
    "ja-JP": "リモート GPU に接続しました",
    "ko-KR": "원격 GPU가 연결되었습니다",
  },
  "远端控制连接中断，实例可能仍在计费": {
    "zh-CN": "远端控制连接中断，实例可能仍在计费",
    "en-US": "Remote control connection lost; the instance may still be billing",
    "zh-TW": "遠端控制連線中斷，執行個體可能仍在計費",
    "zh-HK": "遠端控制連線中斷，實例可能仍在扣費",
    "ja-JP": "リモート制御接続が中断しました。インスタンスが課金継続中の可能性があります",
    "ko-KR": "원격 제어 연결이 끊겼습니다. 인스턴스가 계속 과금 중일 수 있습니다",
  },
  "当前客户端管理的 GPU 已停止": {
    "zh-CN": "当前客户端管理的 GPU 已停止",
    "en-US": "Managed remote GPU stopped",
    "zh-TW": "本程式管理的遠端 GPU 已停止",
    "zh-HK": "本程式管理的遠端 GPU 已停止",
    "ja-JP": "管理中のリモート GPU が停止しました",
    "ko-KR": "관리 중인 원격 GPU가 중지되었습니다",
  },
  "正在释放远端 GPU": {
    "zh-CN": "正在释放远端 GPU",
    "en-US": "Releasing remote GPU",
    "zh-TW": "正在釋放遠端 GPU",
    "zh-HK": "正在釋放遠端 GPU",
    "ja-JP": "リモート GPU を解放しています",
    "ko-KR": "원격 GPU를 해제하고 있습니다",
  },
  "请先完成 Google 登录": {
    "zh-CN": "请先完成 Google 登录",
    "en-US": "Please sign in with Google first",
    "zh-TW": "請先完成 Google 登入",
    "zh-HK": "請先完成 Google 登入",
    "ja-JP": "先に Google ログインを完了してください",
    "ko-KR": "먼저 Google 로그인을 완료해 주세요",
  },
  "Google 授权已失效，请在设置中重新登录": {
    "zh-CN": "Google 授权已失效，请在设置中重新登录",
    "en-US": "Google authorization expired; please sign in again in Settings",
    "zh-TW": "Google 授權已失效，請前往設定重新登入",
    "zh-HK": "Google 授權已失效，請在設定中重新登入",
    "ja-JP": "Google 認可の期限が切れました。設定から再ログインしてください",
    "ko-KR": "Google 인증이 만료되었습니다. 설정에서 다시 로그인해 주세요",
  },
  "Google 授权已取消，请重新登录": {
    "zh-CN": "Google 授权已取消，请重新登录",
    "en-US": "Google authorization was cancelled; please sign in again",
    "zh-TW": "Google 授權已取消，請重新登入",
    "zh-HK": "Google 授權已取消，請重新登入",
    "ja-JP": "Google 認可がキャンセルされました。再度ログインしてください",
    "ko-KR": "Google 인증이 취소되었습니다. 다시 로그인해 주세요",
  },
  "切换账号前请先释放当前实例": {
    "zh-CN": "切换账号前请先释放当前实例",
    "en-US": "Release the active instance before switching Google accounts",
    "zh-TW": "切換帳戶前請先釋放目前的執行個體",
    "zh-HK": "切換帳戶前請先釋放目前實例",
    "ja-JP": "アカウントを切り替える前に現在のインスタンスを解放してください",
    "ko-KR": "계정을 전환하기 전에 현재 인스턴스를 먼저 해제해 주세요",
  },
  "正在停止运行中的对外 API 以吊销旧密钥": {
    "zh-CN": "正在停止运行中的对外 API 以吊销旧密钥",
    "en-US": "Stopping active Public API to revoke the old key",
    "zh-TW": "正在停止運作中的對外 API 以撤銷舊金鑰",
    "zh-HK": "正在停止運行中的對外 API 以吊銷舊金鑰",
    "ja-JP": "旧キーを無効化するため稼働中の外部 API を停止しています",
    "ko-KR": "기존 키를 폐기하기 위해 실행 중인 외부 API를 중지하는 중",
  },
  "API Key 已更换；重新开启服务后使用新密钥": {
    "zh-CN": "API Key 已更换；重新开启服务后使用新密钥",
    "en-US": "API key rotated; restart the Public API to activate the new key",
    "zh-TW": "API 金鑰已更換；重新開啟服務後將使用新金鑰",
    "zh-HK": "API Key 已更換；重新開啟服務後會使用新金鑰",
    "ja-JP": "API キーを再生成しました。サービス再開後から新しいキーが有効になります",
    "ko-KR": "API 키가 재발급되었습니다. 서비스를 다시 시작하면 새 키가 적용됩니다",
  },
  "31B 稠密 / Q8_0": {
    "zh-CN": "31B 稠密 / Q8_0",
    "en-US": "31B Dense / Q8_0",
    "zh-TW": "31B 稠密 / Q8_0",
    "zh-HK": "31B 稠密 / Q8_0",
    "ja-JP": "31B Dense / Q8_0",
    "ko-KR": "31B 밀집형 / Q8_0",
  },
  "27B 稠密 / BF16": {
    "zh-CN": "27B 稠密 / BF16",
    "en-US": "27B Dense / BF16",
    "zh-TW": "27B 稠密 / BF16",
    "zh-HK": "27B 稠密 / BF16",
    "ja-JP": "27B Dense / BF16",
    "ko-KR": "27B 밀집형 / BF16",
  },
  "320B-A18B MoE / GSQ-RCO-Q4 / 部分专家放内存": {
    "zh-CN": "320B-A18B MoE / GSQ-RCO-Q4 / 部分专家放内存",
    "en-US": "320B-A18B MoE / GSQ-RCO-Q4 / Expert Offload to RAM",
    "zh-TW": "320B-A18B MoE / GSQ-RCO-Q4 / 部分專家卸載至記憶體",
    "zh-HK": "320B-A18B MoE / GSQ-RCO-Q4 / 部分專家放記憶體",
    "ja-JP": "320B-A18B MoE / GSQ-RCO-Q4 / 一部エキスパート RAM オフロード",
    "ko-KR": "320B-A18B MoE / GSQ-RCO-Q4 / 일부 전문가 RAM 오프로드",
  },
  "远端引导脚本已启动，正在读取启动配置": {
    "zh-CN": "远端引导脚本已启动，正在读取启动配置",
    "en-US": "Remote bootstrap started; reading launch configuration",
    "zh-TW": "遠端啟動腳本已啟動，正在讀取啟動設定",
    "zh-HK": "遠端引導腳本已啟動，正在讀取啟動設定",
    "ja-JP": "リモート起動スクリプトを開始しました。設定を読み込んでいます",
    "ko-KR": "원격 부트스트랩 스크립트가 시작되어 설정을 읽고 있습니다",
  },
  "Google 授权无法自动续期，请在设置中重新登录": {
    "zh-CN": "Google 授权无法自动续期，请在设置中重新登录",
    "en-US": "Google authorization could not be renewed automatically; please sign in again in Settings",
    "zh-TW": "Google 授權無法自動續期，請前往設定重新登入",
    "zh-HK": "Google 授權無法自動續期，請在設定中重新登入",
    "ja-JP": "Google 認可を自動更新できませんでした。設定から再ログインしてください",
    "ko-KR": "Google 인증을 자동으로 갱신할 수 없습니다. 설정에서 다시 로그인해 주세요",
  },
  "Google 登录已保存到本机，后续自动续期": {
    "zh-CN": "Google 登录已保存到本机，后续自动续期",
    "en-US": "Google sign-in saved locally; tokens will renew automatically",
    "zh-TW": "Google 登入已儲存於本機，後續自動續期",
    "zh-HK": "Google 登入已儲存至本地，後續自動續期",
    "ja-JP": "Google ログインを保存しました（以降は自動更新されます）",
    "ko-KR": "Google 로그인이 로컬에 저장되었으며 이후 자동으로 갱신됩니다",
  },
  "已启动本地回调服务，等待浏览器完成 Google 授权": {
    "zh-CN": "已启动本地回调服务，等待浏览器完成 Google 授权",
    "en-US": "Local callback listener started; waiting for Google authorization in browser",
    "zh-TW": "已啟動本機回呼服務，等待瀏覽器完成 Google 授權",
    "zh-HK": "已啟動本地回調服務，等待瀏覽器完成 Google 授權",
    "ja-JP": "ローカルコールバック待機を開始しました。ブラウザで Google 認可を完了してください",
    "ko-KR": "로컬 콜백 서버가 시작되었습니다. 브라우저에서 Google 인증을 완료해 주세요",
  },
  "完成 Google 登录": {
    "zh-CN": "完成 Google 登录",
    "en-US": "Completing Google sign-in",
    "zh-TW": "完成 Google 登入",
    "zh-HK": "完成 Google 登入",
    "ja-JP": "Google ログインを完了しています",
    "ko-KR": "Google 로그인을 완료하는 중",
  },
  "请先启动远端 GPU": {
    "zh-CN": "请先启动远端 GPU",
    "en-US": "Please start a remote GPU first",
    "zh-TW": "請先啟動遠端 GPU",
    "zh-HK": "請先啟動遠端 GPU",
    "ja-JP": "先にリモート GPU を起動してください",
    "ko-KR": "먼저 원격 GPU를 시작해 주세요",
  },
  "请先下载模型到当前远端实例": {
    "zh-CN": "请先下载模型到当前远端实例",
    "en-US": "Please download the model to the active remote instance first",
    "zh-TW": "請先將模型下載至目前的遠端執行個體",
    "zh-HK": "請先下載模型到目前遠端實例",
    "ja-JP": "先に現在のリモートインスタンスへモデルをダウンロードしてください",
    "ko-KR": "먼저 현재 원격 인스턴스에 모델을 다운로드해 주세요",
  },
  "请先停止当前任务": {
    "zh-CN": "请先停止当前任务",
    "en-US": "Please stop the current running task first",
    "zh-TW": "請先停止目前任務",
    "zh-HK": "請先停止目前任務",
    "ja-JP": "先に実行中のタスクを停止してください",
    "ko-KR": "먼저 현재 실행 중인 작업을 중지해 주세요",
  },
  "未检测到可用远端 GPU": {
    "zh-CN": "未检测到可用远端 GPU",
    "en-US": "No available remote GPU detected",
    "zh-TW": "未偵測到可用的遠端 GPU",
    "zh-HK": "未檢測到可用遠端 GPU",
    "ja-JP": "利用可能なリモート GPU が検出されませんでした",
    "ko-KR": "사용 가능한 원격 GPU를 감지할 수 없습니다",
  },
  "正在停止 ComfyUI 服务以释放显存": {
    "zh-CN": "正在停止 ComfyUI 服务以释放显存",
    "en-US": "Stopping ComfyUI service to free VRAM",
    "zh-TW": "正在停止 ComfyUI 服務以釋放 VRAM",
    "zh-HK": "正在停止 ComfyUI 服務以釋放顯存",
    "ja-JP": "VRAM を解放するため ComfyUI サービスを停止しています",
    "ko-KR": "VRAM을 확보하기 위해 ComfyUI 서비스를 중지하는 중",
  },
  "混合装载到显存与内存": {
    "zh-CN": "混合装载到显存与内存",
    "en-US": "Hybrid loading across GPU VRAM and system RAM",
    "zh-TW": "混合載入至 VRAM 與系統記憶體",
    "zh-HK": "混合載入至顯存與系統記憶體",
    "ja-JP": "VRAM とシステム RAM へハイブリッドロード中",
    "ko-KR": "GPU VRAM 및 시스템 RAM 하이브리드 로드 중",
  },
  "加载到远端显存": {
    "zh-CN": "加载到远端显存",
    "en-US": "Loading weights into remote VRAM",
    "zh-TW": "正在載入至遠端 VRAM",
    "zh-HK": "正在載入至遠端顯存",
    "ja-JP": "リモート VRAM へモデルを読み込んでいます",
    "ko-KR": "원격 VRAM에 모델을 로드하는 중",
  },
  "模型体积大于当前显存，已自动启用 GPU 显存 + 系统内存混合装载（--fit on），溢出权重将自动分配至内存": {
    "zh-CN": "模型体积大于当前显存，已自动启用 GPU 显存 + 系统内存混合装载（--fit on），溢出权重将自动分配至内存",
    "en-US": "Model exceeds available VRAM; automatically enabled GPU VRAM + system RAM hybrid loading (--fit on)",
    "zh-TW": "模型體積大於目前 VRAM，已自動啟用 GPU VRAM + 系統記憶體混合載入（--fit on），超出權重將自動分配至記憶體",
    "zh-HK": "模型體積大於目前顯存，已自動啟用 GPU 顯存 + 系統記憶體混合載入（--fit on），溢出權重會自動分配至記憶體",
    "ja-JP": "モデルサイズが現在の VRAM を超えるため、GPU VRAM + システム RAM のハイブリッドロード（--fit on）を自動有効化しました",
    "ko-KR": "모델 크기가 현재 VRAM을 초과하여 GPU VRAM + 시스템 RAM 하이브리드 로드(--fit on)를 자동으로 활성화했습니다",
  },
  "模型加载成功，远端推理服务已就绪": {
    "zh-CN": "模型加载成功，远端推理服务已就绪",
    "en-US": "Model loaded successfully; remote inference service is ready",
    "zh-TW": "模型載入成功，遠端推論服務已就緒",
    "zh-HK": "模型載入成功，遠端推理服務已就緒",
    "ja-JP": "モデルのロードが完了し、リモート推論サービスの準備が整いました",
    "ko-KR": "모델 로드 완료, 원격 추론 서비스가 준비되었습니다",
  },
  "模型加载失败": {
    "zh-CN": "模型加载失败",
    "en-US": "Model load failed",
    "zh-TW": "模型載入失敗",
    "zh-HK": "模型載入失敗",
    "ja-JP": "モデルのロードに失敗しました",
    "ko-KR": "모델 로드에 실패했습니다",
  },
  "模型加载超过 10 分钟": {
    "zh-CN": "模型加载超过 10 分钟",
    "en-US": "Model load timed out after 10 minutes",
    "zh-TW": "模型載入超過 10 分鐘",
    "zh-HK": "模型載入超過 10 分鐘",
    "ja-JP": "モデルのロードが 10 分を超過しました",
    "ko-KR": "모델 로드가 10분을 초과했습니다",
  },
  "实例正在停止，已取消模型加载": {
    "zh-CN": "实例正在停止，已取消模型加载",
    "en-US": "Instance is stopping; model load cancelled",
    "zh-TW": "執行個體正在停止，已取消模型載入",
    "zh-HK": "實例正在停止，已取消模型載入",
    "ja-JP": "インスタンス停止中のためモデルのロードをキャンセルしました",
    "ko-KR": "인스턴스가 중지 중이어서 모델 로드를 취소했습니다",
  },
  "正在预热 GPU 推理内核": {
    "zh-CN": "正在预热 GPU 推理内核",
    "en-US": "Warming up GPU inference kernels",
    "zh-TW": "正在預熱 GPU 推論核心",
    "zh-HK": "正在預熱 GPU 推理核心",
    "ja-JP": "GPU 推論カーネルをウォームアップ中",
    "ko-KR": "GPU 추론 커널을 예열하는 중",
  },
  "正在预热远端 CUDA 推理内核，避免首次 API 调用超时": {
    "zh-CN": "正在预热远端 CUDA 推理内核，避免首次 API 调用超时",
    "en-US": "Warming up remote CUDA kernels to prevent first-request API timeout",
    "zh-TW": "正在預熱遠端 CUDA 推論核心，避免首次 API 呼叫逾時",
    "zh-HK": "正在預熱遠端 CUDA 推理核心，避免首次 API 調用逾時",
    "ja-JP": "初回 API リクエストのタイムアウトを防ぐためリモート CUDA カーネルをウォームアップしています",
    "ko-KR": "첫 API 요청 시간 초과를 방지하기 위해 원격 CUDA 커널을 예열하고 있습니다",
  },
  "已卸载远端模型并释放显存": {
    "zh-CN": "已卸载远端模型并释放显存",
    "en-US": "Unloaded remote model and freed VRAM",
    "zh-TW": "已卸載遠端模型並釋放 VRAM",
    "zh-HK": "已卸載遠端模型並釋放顯存",
    "ja-JP": "リモートモデルをアンロードして VRAM を解放しました",
    "ko-KR": "원격 모델을 언로드하고 VRAM을 해제했습니다",
  },
  "已向远端发送任务取消指令": {
    "zh-CN": "已向远端发送任务取消指令",
    "en-US": "Sent task cancellation request to remote runtime",
    "zh-TW": "已向遠端送出任務取消指令",
    "zh-HK": "已向遠端發送任務取消指令",
    "ja-JP": "リモートへタスクキャンセル指示を送信しました",
    "ko-KR": "원격 런타임에 작업 취소 요청을 전송했습니다",
  },
  "已保存对外 API 固定域名与隧道配置": {
    "zh-CN": "已保存对外 API 固定域名与隧道配置",
    "en-US": "Saved Public API custom domain and tunnel configuration",
    "zh-TW": "已儲存對外 API 固定網域與通道設定",
    "zh-HK": "已儲存對外 API 固定域名與隧道配置",
    "ja-JP": "外部 API の固定ドメインとトンネル設定を保存しました",
    "ko-KR": "외부 API 고정 도메인 및 터널 설정을 저장했습니다",
  },
  "已保存 ComfyUI 原生画布固定域名与隧道配置": {
    "zh-CN": "已保存 ComfyUI 原生画布固定域名与隧道配置",
    "en-US": "Saved ComfyUI Native Canvas domain and tunnel configuration",
    "zh-TW": "已儲存 ComfyUI 原生畫布固定網域與通道設定",
    "zh-HK": "已儲存 ComfyUI 原生畫布固定域名與隧道配置",
    "ja-JP": "ComfyUI ネイティブキャンバスの固定ドメインとトンネル設定を保存しました",
    "ko-KR": "ComfyUI 네이티브 캔버스 고정 도메인 및 터널 설정을 저장했습니다",
  },
  "已刷新云盘工作区与远端状态": {
    "zh-CN": "已刷新云盘工作区与远端状态",
    "en-US": "Refreshed Google Drive workspace and remote status",
    "zh-TW": "已重新整理雲端硬碟工作區與遠端狀態",
    "zh-HK": "已重新整理雲端硬碟工作區與遠端狀態",
    "ja-JP": "Google ドライブのワークスペースとリモート状態を更新しました",
    "ko-KR": "Google Drive 워크스페이스 및 원격 상태를 새로고침했습니다",
  },
  "Google 云盘同步已完成 (ColabOne_Drive)": {
    "zh-CN": "Google 云盘同步已完成 (ColabOne_Drive)",
    "en-US": "Google Drive sync completed (ColabOne_Drive)",
    "zh-TW": "Google 雲端硬碟同步完成 (ColabOne_Drive)",
    "zh-HK": "Google 雲端硬碟同步完成 (ColabOne_Drive)",
    "ja-JP": "Google ドライブの同期が完了しました (ColabOne_Drive)",
    "ko-KR": "Google Drive 동기화가 완료되었습니다 (ColabOne_Drive)",
  },
  "这个仓库需要访问权限，请在设置中保存有权限的 Hugging Face Token": {
    "zh-CN": "这个仓库需要访问权限，请在设置中保存有权限的 Hugging Face Token",
    "en-US": "This repository requires access; please save an authorized Hugging Face token in Settings",
    "zh-TW": "此儲存庫需要存取權限，請在設定中儲存具權限的 Hugging Face Token",
    "zh-HK": "此倉庫需要存取權限，請在設定中儲存有權限的 Hugging Face Token",
    "ja-JP": "このリポジトリにはアクセス権が必要です。設定で権限のある Hugging Face Token を保存してください",
    "ko-KR": "이 저장소는 접근 권한이 필요합니다. 설정에서 권한이 있는 Hugging Face 토큰을 저장해 주세요",
  },
  "Hugging Face 请求较多，请稍后重试": {
    "zh-CN": "Hugging Face 请求较多，请稍后重试",
    "en-US": "Hugging Face rate limit reached; please try again shortly",
    "zh-TW": "Hugging Face 請求次數過多，請稍後再試",
    "zh-HK": "Hugging Face 請求過多，請稍後重試",
    "ja-JP": "Hugging Face へのリクエストが集中しています。しばらくしてから再試行してください",
    "ko-KR": "Hugging Face 요청 한도에 도달했습니다. 잠시 후 다시 시도해 주세요",
  },
  "无法连接 Hugging Face，请检查网络后重试": {
    "zh-CN": "无法连接 Hugging Face，请检查网络后重试",
    "en-US": "Cannot reach Hugging Face; please check your network and retry",
    "zh-TW": "無法連線至 Hugging Face，請檢查網路後重試",
    "zh-HK": "無法連接 Hugging Face，請檢查網絡後重試",
    "ja-JP": "Hugging Face に接続できません。ネットワークを確認して再試行してください",
    "ko-KR": "Hugging Face에 연결할 수 없습니다. 네트워크를 확인한 뒤 다시 시도해 주세요",
  },
  "缺少完整分片或 SHA-256 元数据": {
    "zh-CN": "缺少完整分片或 SHA-256 元数据",
    "en-US": "Missing complete shards or SHA-256 metadata",
    "zh-TW": "缺少完整分片或 SHA-256 中繼資料",
    "zh-HK": "缺少完整分片或 SHA-256 元數據",
    "ja-JP": "分割ファイルまたは SHA-256 メタデータが不完全です",
    "ko-KR": "분할 파일 또는 SHA-256 메타데이터가 누락되었습니다",
  },
  "该文件是量化校准或辅助文件，不是可直接装载的模型权重": {
    "zh-CN": "该文件是量化校准或辅助文件，不是可直接装载的模型权重",
    "en-US": "This file is a calibration or auxiliary artifact, not runnable model weights",
    "zh-TW": "此檔案為量化校準或輔助檔案，非可直接載入的模型權重",
    "zh-HK": "該檔案是量化校準或輔助檔案，並非可直接載入的模型權重",
    "ja-JP": "このファイルはキャリブレーションまたは補助ファイルであり、実行可能なモデル本体ではありません",
    "ko-KR": "이 파일은 양자화 보정 또는 보조 파일로, 직접 로드할 수 있는 모델 가중치가 아닙니다",
  },
  "该模型属于图片/视频生成类（ComfyUI 专用），无法在对外文本 API 中装载，请前往 ComfyUI 工作区使用": {
    "zh-CN": "该模型属于图片/视频生成类（ComfyUI 专用），无法在对外文本 API 中装载，请前往 ComfyUI 工作区使用",
    "en-US": "This is an image/video generation model (ComfyUI only) and cannot be loaded in the Public Text API; use the ComfyUI workspace",
    "zh-TW": "此模型屬於圖片/影片生成類（ComfyUI 專用），無法在對外文字 API 中載入，請前往 ComfyUI 工作區使用",
    "zh-HK": "該模型屬於圖片/影片生成類（ComfyUI 專用），無法在對外文字 API 中載入，請前往 ComfyUI 工作區使用",
    "ja-JP": "このモデルは画像・動画生成用（ComfyUI 専用）のため外部テキスト API ではロードできません。ComfyUI ワークスペースをご利用ください",
    "ko-KR": "이 모델은 이미지/비디오 생성 전용(ComfyUI 전용)으로 외부 텍스트 API에서 로드할 수 없습니다. ComfyUI 워크스페이스에서 사용해 주세요",
  },
  "实例清单已变化，请重新点击停止": {
    "zh-CN": "实例清单已变化，请重新点击停止",
    "en-US": "Instance list changed; please click Stop again to review",
    "zh-TW": "執行個體清單已變更，請重新點擊停止",
    "zh-HK": "實例清單已變動，請重新點擊停止",
    "ja-JP": "インスタンス一覧が変更されました。もう一度停止をクリックして確認してください",
    "ko-KR": "인스턴스 목록이 변경되었습니다. 중지 버튼을 다시 눌러 확인해 주세요",
  },
  "不支持的语言选项": {
    "zh-CN": "不支持的语言选项",
    "en-US": "Unsupported language selection",
    "zh-TW": "不支援的語言選項",
    "zh-HK": "不支援的語言選項",
    "ja-JP": "サポートされていない言語設定です",
    "ko-KR": "지원하지 않는 언어 설정입니다",
  },
  "关闭对外 API": {
    "zh-CN": "关闭对外 API",
    "en-US": "Stopping Public API",
    "zh-TW": "關閉對外 API",
    "zh-HK": "關閉對外 API",
    "ja-JP": "外部 API を停止中",
    "ko-KR": "외부 API 중지 중",
  },
  "停止远端 ComfyUI": {
    "zh-CN": "停止远端 ComfyUI",
    "en-US": "Stopping remote ComfyUI",
    "zh-TW": "停止遠端 ComfyUI",
    "zh-HK": "停止遠端 ComfyUI",
    "ja-JP": "リモート ComfyUI を停止中",
    "ko-KR": "원격 ComfyUI 중지 중",
  },
  "关闭 ComfyUI 原生画布": {
    "zh-CN": "关闭 ComfyUI 原生画布",
    "en-US": "Stopping ComfyUI Native Canvas",
    "zh-TW": "關閉 ComfyUI 原生畫布",
    "zh-HK": "關閉 ComfyUI 原生畫布",
    "ja-JP": "ComfyUI ネイティブキャンバスを停止中",
    "ko-KR": "ComfyUI 네이티브 캔버스 중지 중",
  },
  "ComfyUI 原生画布已关闭": {
    "zh-CN": "ComfyUI 原生画布已关闭",
    "en-US": "ComfyUI Native Canvas stopped",
    "zh-TW": "ComfyUI 原生畫布已關閉",
    "zh-HK": "ComfyUI 原生畫布已關閉",
    "ja-JP": "ComfyUI ネイティブキャンバスを停止しました",
    "ko-KR": "ComfyUI 네이티브 캔버스가 중지되었습니다",
  },
};

interface PatternRule {
  re: RegExp;
  format: (m: RegExpMatchArray, locale: ColabOneLocale) => string;
}

const PATTERN_RULES: PatternRule[] = [
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
    re: /^正在启动 (\S+) GPU/,
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
  {
    re: /^远端设备 (.+?)，显存 ([\d.]+) GiB(?:，模型权重 ([\d.]+) GiB)?$/,
    format: (m, loc) => {
      const [, gpuName, vram, weight] = m;
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
    re: /^模型加载失败：(.+)$/,
    format: (m, loc) => {
      const detail = m[1];
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
    re: /^正在重新装载 (.+)，先卸载当前实例以释放显存$/,
    format: (m, loc) => {
      const name = m[1];
      switch (loc) {
        case "en-US":
          return `Reloading ${name}; unloading current instance first to free VRAM`;
        case "zh-TW":
          return `正在重新載入 ${name}，先卸載目前執行個體以釋放 VRAM`;
        case "zh-HK":
          return `正在重新載入 ${name}，先卸載目前實例以釋放顯存`;
        case "ja-JP":
          return `${name} を再ロード中（VRAM 解放のため既存プロセスを停止しています）`;
        case "ko-KR":
          return `${name} 다시 로드 중 (VRAM 확보를 위해 기존 인스턴스 언로드)`;
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
];

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
  const exactPlan: Record<string, LocaleTable> = {
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
  };
  return exactPlan[s]?.[loc] ?? s;
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
  const trimmed = raw
    .trim()
    .replace(/^(?:(?:Error|CallError|RuntimeError|TypeError|ValueError):\s*)+/i, "")
    .trim();
  if (!trimmed || locale === "zh-CN") return trimmed || raw;
  const exact = EXACT_MESSAGES[trimmed];
  if (exact) return exact[locale];
  for (const rule of PATTERN_RULES) {
    const m = trimmed.match(rule.re);
    if (m) return rule.format(m, locale);
  }
  return trimmed;
}
