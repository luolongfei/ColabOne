<div align="center">
  <img src="console/resources/icon.png" width="96" height="96" alt="ColabOne" />
  <h1>ColabOne</h1>
  <p><strong>輕量級 Google Colab GPU 桌面工作區 — 遠端部署 OpenAI 相容 GGUF 大語言模型，並執行 ComfyUI 工作流。</strong></p>

  <p>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-GPL--3.0-2563eb?style=flat-square" alt="授權協議：GPL-3.0" /></a>
    <img src="https://img.shields.io/badge/Platform-macOS%20Apple%20Silicon%20%7C%20Windows%20x64-475569?style=flat-square" alt="支援平台：macOS Apple Silicon | Windows x64" />
    <img src="https://img.shields.io/badge/OpenAI%20API-Responses%20%26%20Chat-0f766e?style=flat-square" alt="OpenAI Responses 與 Chat API" />
    <img src="https://img.shields.io/badge/ComfyUI-v0.46.0-7c3aed?style=flat-square" alt="ComfyUI v0.46.0" />
  </p>

  <p>
    <a href="README.md">English</a> ·
    <a href="README.zh-CN.md">简体中文</a> ·
    <a href="README.zh-TW.md">繁體中文（台灣）</a> ·
    <strong>繁體中文（香港）</strong> ·
    <a href="README.ja-JP.md">日本語</a> ·
    <a href="README.ko-KR.md">한국어</a>
  </p>
</div>

---

ColabOne 為 **macOS（Apple Silicon）** 及 **Windows（x64）** 提供流暢的原生桌面應用程式，讓你直接管理遠端 Google Colab GPU。無需折騰 Notebook，即可一鍵啟動並管理多部 GPU 實例、即時監控硬件負載與計費續航、架設私密 OpenAI 相容大語言模型介面，並執行 ComfyUI 工作流。

你的電腦只會運行單一純 Go 原生桌面執行檔（`ColabOne.app` / `ColabOne.exe`），所有本地控制邏輯均內置於同一進程中 —— 本地完全無需 Python 執行環境。所有模型下載、GGUF 權重、`llama.cpp` 推理及 ComfyUI 運算均只在遠端 Colab GPU 上執行（由啟動時自動上傳的內嵌式 Python 遠端控制服務驅動）。

## ✨ 核心亮點

- **多 GPU 管理與即時硬件監控（`遠端裝置`）** — 按需啟動、切換及選擇性釋放多部 Colab GPU 實例（`T4`、`L4`、`A100`、`H100`、`G4` RTX PRO 6000），同時檢視同一 Google 帳戶下其他來源的運行實例。即時掌握計費狀態、`CU/小時` 消耗速率、剩餘 CU 餘額、預計可用時長、地區國旗標示，以及涵蓋 GPU 顯存／核心負載、系統記憶體、磁碟 I/O 與 CPU 的 60 秒即時曲線圖。
- **智能閒置自動釋放** — 透過可自訂的閒置倒數（預設 **30 分鐘**，可選 `15 分鐘` 至 `2 小時`）保護你的計算單元餘額。機制直接由遠端 VM 執行，即使結束 ColabOne 程式依然繼續生效；於模型下載、載入顯存、對外 API 推理及 ComfyUI 任務期間自動暫停倒數。
- **OpenAI 相容大語言模型網關（`對外 API`）** — 直接在內置的**選擇模型**面板瀏覽並載入 Hugging Face GGUF 模型，提供即時顯存與磁碟適配評估、自動顯存 + 系統記憶體混合載入（`--fit on`）、16／32 線程並發並經 SHA-256 校驗的高速下載，以及 `8K`–`128K` 上下文檔位。透過你自己的 Cloudflare Named Tunnel 對外提供 `/v1/models`、`/v1/chat/completions`、`/v1/responses` 及 `/v1/responses/compact` 介面。
- **ComfyUI 工作流工坊與原生畫布（`ComfyUI`）** — 以雙模式運行遠端 **ComfyUI `v0.46.0`**：**工作流工坊**可將任何 ComfyUI API JSON 自動轉換為簡潔的參數表單，支援經 SHA-256 校驗的線上預覽與匯出至本地；**原生畫布**則透過獨立令牌認證的安全隧道，在專屬桌面視窗或系統瀏覽器開啟完整節點編輯器。
- **Google 雲端硬碟自動持久保存** — 以僅限本程式存取的 `drive.file` 權限，自動將模型清單、ComfyUI 工作流與輸出作品保存至 `My Drive / ColabOne_Drive`。開箱即支援 Colab 原生 DriveFS（每次新建實例只需於瀏覽器點擊確認一次），在免費同意一次 Google Cloud 服務條款後，更可升級為永久免彈窗的無頭 `rclone` 自動掛載。
- **為日常使用而設** — 採用純 Go 單一執行檔架構（MyGo `v0.10.0` + 進程內 Go 控制器，無需本地 Python 執行環境或同層 `runtime/`、`site-packages/` 目錄，亦無需在本地安裝 Python、Go 或 Node），支援 6 種介面語言、淺色／深色／跟隨系統外觀主題、本地 `0600` AES-256-GCM 加密憑證檔案儲存（`vault.enc`，無系統鑰匙圈彈窗）、可選的 GPU 分配代理（僅分配階段）、一鍵遠端服務熱更新，以及即時脫敏的**執行日誌**面板（`Ctrl+J`）。

## 🚀 快速上手

ColabOne 採用四個側邊欄分頁設計：**遠端裝置**、**對外 API**、**ComfyUI** 及**設定**，並於右側配備可滑出展開的**執行日誌**面板。

1. **下載並安裝**
   從 [Releases](https://github.com/luolongfei/ColabOne/releases) 下載對應平台的安裝程式（或[從源碼構建](#-從源碼構建)）：Windows 雙擊執行 `.exe` 安裝程式（安裝至目前用戶目錄，無需管理員權限），macOS 打開 `.dmg` 磁碟映像並將 `ColabOne.app` 拖入**應用程式**。ColabOne 是完全自包含的純 Go 單一執行檔應用程式，無需本地 Python 執行環境，亦無需保留同層的 `runtime/` 或 `site-packages/` 資料夾。於 Windows 平台，亦可隨時於**設定**內管理開始選單及桌面捷徑，無需管理員權限。
   - *首次開啟提示（開源構建未購買商業簽署，僅需操作一次）*：**macOS** 若提示「未開啟／無法驗證」，點擊**完成**，開啟**系統設定 / 私隱與安全性**，底部點擊**強制打開**，或在終端機執行 `xattr -dr com.apple.quarantine /Applications/ColabOne.app`；**Windows** 若出現 SmartScreen 提示，點擊**其他資訊 / 仍要執行**。

2. **登入 Google 帳戶**
   前往**設定 / 登入 Google**，在瀏覽器完成帳戶授權後，ColabOne 會自動完成登入。如果瀏覽器顯示授權頁內容而未自動跳轉，可展開手動貼上區域並點擊**貼上並完成登入**。登入憑證會加密儲存在本地設定目錄的 `vault.enc`（`0600` 權限 + AES-256-GCM）中，不觸發系統鑰匙圈彈窗，重啟程式會自動恢復並續期。

3. **啟動 GPU**
   打開**遠端裝置**，選擇 GPU 型號（預設為 `G4`）及閒置自動釋放時長（預設 **30 分鐘**），再點擊**啟動 GPU**。實例就緒後，頂部狀態欄及概覽卡片會即時顯示計費續航、實例位置、硬件監控曲線圖及 Google 雲端硬碟掛載狀態。

4. **架設大語言模型或執行 ComfyUI**
   - **文字 API**：打開**對外 API / 選擇模型**，從**我的**（已預置可編輯的入門 Hugging Face 倉庫）挑選 GGUF 模型，或在**社群**搜尋。點擊**下載並載入**即可在遠端下載並校驗模型分片再載入至 `llama-server`，隨後設定你的 Cloudflare Named Tunnel 並開啟 API 服務。
   - **視覺與多模態工作流**：打開 **ComfyUI**，在**工作流工坊**匯入工作流，或在**原生畫布**開啟完整節點編輯器。

5. **完成後釋放實例**
   可針對個別實例點擊**單獨停止**，或點擊**停止全部**自由勾選需要終止的實例，其餘工作階段不受影響。由本程式管理的實例在釋放前會自動關閉公網隧道，並將數據完整寫回 `ColabOne_Drive`。

## 🌐 OpenAI 相容對外 API

**對外 API** 結合內置的單模型 GGUF 選擇器，以及由遠端 `llama-server`（`127.0.0.1:8081`）驅動、採用 Bearer 認證的專屬 OpenAI 網關（`127.0.0.1:7861`）。所有管理路由（`/api/v1/*`）均嚴格隔離於私有控制連接埠，絕不暴露至公網。

### 📡 支援的 API 介面

| 方法 | 介面路徑 | 說明 |
|---|---|---|
| `GET` | `/v1/models` `/v1/models/{id}` | 回傳目前已載入至遠端顯存的 GGUF 模型（包含啟用中的上下文長度）。 |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions 對話補全介面，支援 `stream=true/false`、工具調用及 `developer` 角色。 |
| `POST` | `/v1/responses` | OpenAI Responses API（`wire_api = "responses"`），支援 SSE 串流、function 與 custom／`apply_patch` 工具調用、推理摘要及三階段自動上下文壓縮。 |
| `POST` | `/v1/responses/compact` | 適用於長篇編程對話的 Codex／OpenAI 上下文壓縮介面（`response.compaction`）。 |

### 🔌 接入外部程式與 SDK

1. **設定固定域名隧道**：在 Cloudflare Zero Trust 建立一個 Cloudflared Named Tunnel，將你的公開 HTTPS 域名指向 `http://127.0.0.1:7861`（請勿為 API 域名開啟 Cloudflare 互動式登入驗證）。將安裝指令或 Tunnel Token 貼上到**對外 API**，點擊**儲存設定**後再點擊**開啟 API 服務**（*程式特意停用 `trycloudflare.com` 臨時域名*）。
2. **將應用程式或 SDK 指向 ColabOne**：複製 **Base URL**、**API Key** 及**目前模型 ID**（支援即時修改而無需重載顯存，亦可使用固定別名 `colabone/auto` 自動路由至目前載入的模型）。介面內已內置 **cURL**、**Python** 與 **Codex** 調用範例，並即時顯示每次請求的 Token 速度（`tok/s`、首字延遲 TTFT、提示詞預填充速度）。

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="https://api.example.com/v1",
    api_key=os.environ["COLABONE_API_KEY"],
)

stream = client.responses.create(
    model="colabone/auto",
    input="你好，來自 ColabOne！",
    stream=True,
)
for event in stream:
    if event.type == "response.output_text.delta":
        print(event.delta, end="", flush=True)
```

關閉**對外 API** 只會撤銷公網存取，不會釋放遠端 GPU，已載入顯存的模型亦會保持熱待機；停止 GPU 實例時則會自動關閉網關與隧道。完整介面規範及錯誤代碼請參閱 [docs/api.md](docs/api.md#public-inference-api-llm-openai-gateway)。

## 🎨 ComfyUI 工作區

ColabOne 只會將 **ComfyUI `v0.46.0`** 部署於遠端 Colab GPU（`127.0.0.1:8188`），並提供雙層工作區體驗：

- **工作流工坊**
  - 支援匯入任何 ComfyUI **Save (API Format)** JSON 工作流——即使尚未啟動 GPU 亦可離線匯入管理——並自動生成具型別約束的參數表單。
  - 支援上傳參考圖片或音訊素材、提交可隨時取消的遠端隊列任務，並即時追蹤節點執行進度。
  - 支援線上預覽並透過系統原生儲存對話框匯出經 SHA-256 校驗的圖片、影片與音訊輸出作品；工作流（`/workflows/**`）及輸出作品（`/comfyui-outputs/**`）會自動同步至 `ColabOne_Drive`。
  - 透過 `catalog/comfyui.json` 以宣告式管理遠端模型與工作流套件（純數據清單，鎖定版本修訂並附帶 SHA-256 校驗）。
- **原生畫布**
  - 在固定域名 Cloudflare Named Tunnel 後方運行具獨立令牌認證的 HTTP + WebSocket 反向代理（`127.0.0.1:8189` 轉發至 `8188`）。
  - 每次工作階段均以隨機令牌換取限定作用域的 `HttpOnly` Cookie，可直接於獨立的 **ComfyUI 原生畫布** 桌面視窗或系統瀏覽器中開啟。
- **嚴格顯存互斥**
  - `llama-server`（`8081`）與 `ComfyUI`（`8188`）絕不會同時佔用 GPU 顯存。切換引擎時會自動卸載目前運行中的服務，同時完整保留遠端磁碟上的模型緩存。
  - 執行中的 ComfyUI 隊列及原生畫布操作會自動暫停閒置自動釋放倒數。

## 🎙️ 數碼人口播與 96 GB 旗艦開源 TTS 引擎矩陣

內置的 ComfyUI 數碼人口播工作流已整合 **8 款旗艦開源 TTS 引擎矩陣**，全部只於遠端 Colab GPU 上隔離運行（專為 96 GB `G4` RTX PRO 6000 及 80 GB `A100` 旗艦規格優化，輕量級引擎亦兼容中小顯存實例）。每個引擎均於隔離的遠端 TTS Worker 子進程內執行（`server/tts_engines.py` / `server/tts_worker_*.py`），內建顯存與磁碟空間前置檢查、按引擎動態分段，以及與 ComfyUI 數碼人影片合成之間的串行全顯存接力移交機制。

### 🧠 96 GB 旗艦開源 TTS 引擎矩陣

| 引擎 | 架構／取樣率與核心特性 | 顯存／磁碟需求與上游授權 |
|---|---|---|
| **Fish Audio Drama 3** | 35B MoE（啟用 3B），44.1 kHz，旗艦級高表現力語音合成 | `>= 45 GiB` 顯存（48 GB 級 Ada/Hopper FP8 GPU；A100 80GB BF16 約 72 GiB） · `>= 48 GiB` 磁碟 · Fish Audio Community License v1.0（**僅限非商用／研究**，禁止託管 TTS API） |
| **MOSS-TTS v1.5** | 8B，24 kHz，支援 31 種語言及內聯 `[pause X.Ys]` 停頓控制標籤 | `>= 20 GiB` 顯存 · `>= 28 GiB` 磁碟 · Apache-2.0 |
| **Breeze TTS 2** | 3B+，24 kHz，中英雙語 Voice Design／聲音克隆／風格指導及副語言標籤（`[笑]`／`[叹气]`） | `>= 12 GiB` 顯存 · `>= 12 GiB` 磁碟 · BreezeBlue Research & Non-Commercial License（**僅限非商用／研究**） |
| **Ming-3.0-TTS Tiny** | 7.9B MoE（啟用 1.3B），44.1 kHz，多方言 + 14 種副語言標籤 | `>= 18 GiB` 顯存 · `>= 22 GiB` 磁碟 · MIT |
| **VoxCPM2** | 2B，48 kHz 錄音室級連續擴散語音合成，支援聲音克隆與文字捏聲線 | `>= 8 GiB` 顯存 · `>= 6 GiB` 磁碟 · Apache-2.0 |
| **Qwen3-TTS 1.7B** | 24 kHz，Base + VoiceDesign + CustomVoice（**9 位官方原生說話人**），多語種支援 | `>= 8 GiB` 顯存 · `>= 15 GiB` 磁碟 · Apache-2.0 |
| **IndexTTS-2.5** | 22.05 kHz，情緒參考音解耦控制 + GRPO 對齊 + 拼音讀音強制覆蓋 | `>= 8 GiB` 顯存 · `>= 15 GiB` 磁碟 · Bilibili Model Use License Agreement（**僅限非商用／研究**） |
| **Fish Speech S2-Pro** | 4B，44.1 kHz，支援高表現力情緒與副語言內聯標籤 | `>= 20 GiB` 顯存 · `>= 16 GiB` 磁碟 · Fish Audio Research License（**僅限非商用／研究**） |

### 🎧 內置音色庫與一鍵短文試聽

- **精選音色陣容**：內置 **10 位精選中文女聲與男聲** 及 **9 位 Qwen3-TTS CustomVoice 官方原生說話人**。
- **一鍵遠端 GPU 即時試聽與緩存**：每位內置音色及官方說話人均配對專屬口播試聽短文，可於 ComfyUI 工作流工坊一鍵觸發遠端 GPU 即時合成試聽，並透過遠端磁碟緩存快速重播，確認聲音效果後再執行完整數碼人影片渲染。

> [!IMPORTANT]
> **非商用及研究用途合規聲明**
>
> 整合的數碼人旗艦 TTS 引擎陣容包含研究／非商業授權的開源權重模型 —— 包括 **Fish Audio Drama 3**（Fish Audio Community License v1.0，禁止商業用途及託管 TTS API 服務）、**Breeze TTS 2**（BreezeBlue Research & Non-Commercial License）、**IndexTTS-2.5**（Bilibili Model Use License Agreement）及 **Fish Speech S2-Pro**（Fish Audio Research License）。因此，**本專案整合的數碼人旗艦 TTS 流水線嚴格僅供個人學習、技術實驗與非商業研究使用 —— 嚴禁作商業用途或託管 TTS 服務轉售。** 所有 TTS 權重均只在需要時下載至你的遠端 Colab 執行環境，並須遵守各自上游授權條款（詳見 [NOTICE](NOTICE)）。

## 🔒 儲存、計費與安全

- **Google 雲端硬碟同步**：模型清單、ComfyUI 工作流與輸出作品均以受限的 `drive.file` 權限持久保存至 `My Drive / ColabOne_Drive`（ColabOne 無法讀取你雲端硬碟上其他無關的個人檔案）。模型權重存放於 Colab 的高速暫存磁碟，每次新建實例均可隨時重新下載；卸載或切換模型絕不會刪除目前實例上已緩存的檔案。
- **系統托盤與自動釋放**：關閉主視窗會將 ColabOne 縮小至系統托盤。結束程式會停止本地心跳，但遠端 VM 上的閒置自動釋放保護會繼續生效。網絡斷線不會停止 Colab 計費——請務必於**遠端裝置**確認實例已停止。
- **計費估算說明**：介面顯示的剩餘 CU 餘額、`CU/小時` 消耗速率及預計可用時長乃根據最近的 Colab 查詢結果推算，與官方結算帳單之間可能存在延遲。
- **憑證與私隱**：Google OAuth 令牌、API 金鑰、Cloudflare Tunnel Token、可選的 Hugging Face Token（用於受限倉庫）及可選的 GPU 分配代理設定（僅分配階段），均加密儲存於本地設定目錄的 `vault.enc`（`0600` 權限 + AES-256-GCM）中，並在日誌中自動脫敏（記憶體保留最近 300 條記錄，另附本地 `0600` 權限活動日誌）。ColabOne 與現有 Colab CLI 設定檔完全隔離、互不干擾。
- **功能範圍與安全校驗**：GGUF 模型匯入會鎖定完整的 40 位 Commit SHA 並對每個分片執行 SHA-256 校驗，絕不執行倉庫內的任何 Python 代碼。舊版內置桌面對話／圖片／影片頁面已退役，由**對外 API** 及 **ComfyUI** 取代。Agent 與本地 MCP 執行不在 v0.1 範圍內。開發版本未經商業代碼簽署或公證——你隨時可以審閱源碼或在本地自行構建。

## 🔧 從源碼構建

### 📋 環境需求

- **Go** `1.27.3`
- **Node.js** `24 LTS`（或 `26`）
- **Python** `3.12` 及 **`uv`**（或標準 `venv` + `pip`）—— 僅用於構建與測試階段（同步內嵌遠端服務包、執行遠端 Python 單元測試及打包腳本）

### 🔨 初始化與構建

```sh
uv venv --python 3.12
uv pip install --python .venv/bin/python -r requirements-dev.txt
.venv/bin/python tools/sync_service_bundle.py
npm --prefix frontend ci
cd console
go tool mygo generate
cd ..
npm --prefix frontend run build
```

### 📦 驗證與打包

> 於 Windows 平台，請將 `.venv/bin/python` 替換為 `.venv\Scripts\python.exe`。

```sh
# 執行全部離線檢查（內嵌服務包同步檢查、Go vet/test、遠端 Python pytest、Vitest 及前端構建）
.venv/bin/python tools/install.py --root work/check --check

# 在 dist/ 目錄打包原生純 Go 安裝檔（Windows 為 .exe，macOS 為 .dmg）
.venv/bin/python tools/package.py --out dist
```

原生安裝檔（`ColabOne-<version>-windows-x64-setup.exe` / `ColabOne-<version>-macos-arm64.dmg`）會輸出至 `dist/`（未壓縮暫存目錄位於 `work/package-stage/Programs/ColabOne`；如另需免安裝便攜壓縮包可加上 `--portable-zip`），內含獨立的純 Go 單一執行檔（`ColabOne.app` / `ColabOne.exe`），並透過 `go:embed` 直接內嵌編譯後的 Vue 前端資源及遠端 Python 控制服務（`server/`、`common/`、`catalog/`），終端用戶無需本地 Python 執行環境或任何同層依賴資料夾。

所有單元測試與瀏覽器測試均 100% 離線使用模擬（Mock）運行——絕不會連接 Google 或 Hugging Face，亦不會申請任何 GPU 實例。真實 GPU 驗收測試會在嚴格的算力預算下明確觸發執行，詳見 [docs/validation.md](docs/validation.md)。

## 📚 技術文件

- [架構概覽](docs/architecture.md) — 純 Go 進程內控制器、內嵌遠端服務、回環連接埠、雲端硬碟同步、硬件監控及安全邊界
- [遠端與對外 API 參考](docs/api.md) — 控制路由、OpenAI 網關規範及 ComfyUI 介面說明
- [驗證記錄](docs/validation.md) — 離線 CI 測試覆蓋率與真實 GPU 驗收日誌
- [上游來源清單](third_party/SOURCES.md) — 鎖定的第三方依賴項目與版權歸屬

## 🤝 參與貢獻

非常歡迎提交貢獻、錯誤回報及功能建議！請參閱[貢獻指南](CONTRIBUTING.md)，了解本地開發環境設定、Pull Request 規範及離線測試要求。

### 💖 貢獻者

感謝每一位協助構建及改進 ColabOne 的貢獻者：

<a href="https://github.com/luolongfei/ColabOne/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=luolongfei/ColabOne" alt="ColabOne 貢獻者" />
</a>

## 🎉 鳴謝

特別感謝 **[Alma](https://alma.now/)** 及 **[Antigravity-Manager](https://github.com/lbjlaq/Antigravity-Manager)** — ColabOne 的介面設計、視覺打磨以至應用程式 Logo，均直接深受這兩個項目的啟發與參考。

同時衷心感謝以下開源項目，正因為有它們的開源分享與底層支撐，才有了今天的 ColabOne（完整詳情見 [NOTICE](NOTICE) 及 [third_party/SOURCES.md](third_party/SOURCES.md)）：

- **[Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga)** *(Apache-2.0)* — 參考其 Google Colab GPU 分配、執行環境就緒檢測及心跳生命週期管理方案。
- **[EasyColab](https://github.com/wy51ai/easycolab)** *(Apache-2.0)* — 參考其桌面端與 Colab 之間的控制架構，以及無頭 `rclone` 掛載 Google 雲端硬碟的實踐模式。
- **[colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service)** *(MIT)* — 參考其模型清單、鎖定版本的 `llama.cpp` CUDA 部署及並發分段下載策略。
- **[llama.cpp](https://github.com/ggml-org/llama.cpp)** *(MIT)* 與 **[ComfyUI](https://github.com/Comfy-Org/ComfyUI)** (`v0.46.0`, GPL-3.0) — 分別作為遠端 GGUF 大語言模型推理引擎，以及節點式圖片、影片與多模態工作流引擎整合。
- **桌面端與前端 UI 技術棧** — **[MyGo](https://github.com/egoist/mygo)** (`v0.10.0`, MIT)、**[coder/websocket](https://github.com/coder/websocket)** *(ISC)*、**[Vue 3](https://github.com/vuejs/core)**、**[Vite](https://github.com/vitejs/vite)**、**[Ant Design Vue](https://github.com/vueComponent/ant-design-vue)**、**[Phosphor Icons](https://github.com/phosphor-icons/vue)**、**[Shiki](https://github.com/shikijs/shiki)**、**[flag-icons](https://github.com/lipis/flag-icons)** *(MIT)* 及 **[Sarasa Gothic](https://github.com/be5invis/Sarasa-Gothic)** *(SIL OFL 1.1)*。
- **遠端服務、隧道與雲端硬碟依賴** — **[FastAPI](https://github.com/fastapi/fastapi)** *(MIT)*、**[Google Colab CLI](https://github.com/googlecolab/google-colab-cli)** *(Apache-2.0)*、**[cloudflared](https://github.com/cloudflare/cloudflared)** *(Apache-2.0)* 及 **[rclone](https://github.com/rclone/rclone)** *(MIT)*。

## 📜 開源授權

ColabOne 採用 **GNU General Public License v3.0**（[LICENSE](LICENSE)）開源授權，以配合遠端整合的 [ComfyUI](https://github.com/Comfy-Org/ComfyUI)（`v0.46.0`，GPL-3.0）。所有上游開源授權與版權聲明均完整保留於 [NOTICE](NOTICE) 及 [third_party/SOURCES.md](third_party/SOURCES.md)。遠端按需下載的數碼人旗艦 TTS 權重須受各自上游授權條款約束，當中包含僅限非商用／研究授權模型（`Fish Audio Drama 3`、`Breeze TTS 2`、`IndexTTS-2.5`、`Fish Speech S2-Pro`），整合的數碼人旗艦 TTS 流水線僅供個人學習、實驗及非商業研究用途。
