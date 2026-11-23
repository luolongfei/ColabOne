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

ColabOne 為 **macOS（Apple Silicon）** 及 **Windows（x64）** 提供流暢的原生桌面應用程式，讓你直接管理遠端 Google Colab GPU。無需折騰 Notebook，亦無需設定 VPN，即可一鍵啟動並管理多部 GPU 實例、即時監控硬件負載與計費續航、架設私密 OpenAI 相容大語言模型介面，並執行 ComfyUI 工作流。

你的電腦只會運行桌面介面及輕量級本地控制進程。所有模型下載、GGUF 權重、`llama.cpp` 推理及 ComfyUI 運算均只在遠端 Colab GPU 上執行。

## ✨ 核心亮點

- **多 GPU 管理與即時硬件監控（`遠端裝置`）** — 按需啟動、切換及選擇性釋放多部 Colab GPU 實例（`T4`、`L4`、`A100`、`H100`、`G4` RTX PRO 6000），同時檢視同一 Google 帳戶下其他來源的運行實例。即時掌握計費狀態、`CU/小時` 消耗速率、剩餘 CU 餘額、預計可用時長、地區國旗標示，以及涵蓋 GPU 顯存／核心負載、系統記憶體、磁碟 I/O 與 CPU 的 60 秒即時曲線圖。
- **智能閒置自動釋放** — 透過可自訂的閒置倒數（預設 **30 分鐘**，可選 `15 分鐘` 至 `2 小時`）保護你的計算單元餘額。機制直接由遠端 VM 執行，即使結束 ColabOne 程式依然繼續生效；於模型下載、載入顯存、對外 API 推理及 ComfyUI 任務期間自動暫停倒數。
- **OpenAI 相容大語言模型網關（`對外 API`）** — 直接在內置的**選擇模型**面板瀏覽並載入 Hugging Face GGUF 模型，提供即時顯存與磁碟適配評估、自動顯存 + 系統記憶體混合載入（`--fit on`）、16／32 線程並發並經 SHA-256 校驗的高速下載，以及 `8K`–`128K` 上下文檔位。透過你自己的 Cloudflare Named Tunnel 對外提供 `/v1/models`、`/v1/chat/completions`、`/v1/responses` 及 `/v1/responses/compact` 介面。
- **ComfyUI 工作流工坊與原生畫布（`ComfyUI`）** — 以雙模式運行遠端 **ComfyUI `v0.46.0`**：**工作流工坊**可將任何 ComfyUI API JSON 自動轉換為簡潔的參數表單，支援經 SHA-256 校驗的線上預覽與匯出至本地；**原生畫布**則透過獨立令牌認證的安全隧道，在專屬桌面視窗或系統瀏覽器開啟完整節點編輯器。
- **Google 雲端硬碟自動持久保存** — 以僅限本程式存取的 `drive.file` 權限，自動將模型清單、ComfyUI 工作流與輸出作品保存至 `My Drive / ColabOne_Drive`。開箱即支援 Colab 原生 DriveFS（每次新建實例只需於瀏覽器點擊確認一次），在免費同意一次 Google Cloud 服務條款後，更可升級為永久免彈窗的無頭 `rclone` 自動掛載。
- **為日常使用而設** — 免安裝便攜包內置獨立 CPython 執行環境（無需在本地安裝 Python、Go 或 Node），支援 6 種介面語言、淺色／深色／跟隨系統外觀主題、本地系統憑證庫加密儲存、可選的 GPU 分配代理（僅分配階段）、一鍵遠端服務熱更新，以及即時脫敏的**執行日誌**面板（`Ctrl+J`）。

## 🚀 快速上手

ColabOne 採用四個側邊欄分頁設計：**遠端裝置 → 對外 API → ComfyUI → 設定**，並於右側配備可滑出展開的**執行日誌**面板。

1. **下載並啟動**
   從 [Releases](https://github.com/luolongfei/ColabOne/releases) 下載對應平台的便攜壓縮包（或[從源碼構建](#-從源碼構建)），完整解壓資料夾後開啟 `ColabOne.app`（macOS）或 `ColabOne.exe`（Windows）。請保留同層目錄（`runtime`、`site-packages`、`bridge` 等）與主程式在同一資料夾內。於 Windows 平台，可在**設定**內直接建立開始選單及桌面捷徑，無需管理員權限。

2. **登入 Google 帳戶**
   前往**設定** → **登入 Google**，在瀏覽器完成帳戶授權後，ColabOne 會自動完成登入。如果瀏覽器顯示授權頁內容而未自動跳轉，可展開手動貼上區域並點擊**貼上並完成登入**。登入憑證會加密儲存在 macOS 鑰匙圈（Keychain）或 Windows 憑證管理員中，重啟程式會自動恢復並續期。

3. **啟動 GPU**
   打開**遠端裝置**，選擇 GPU 型號（預設為 `T4`）及閒置自動釋放時長（預設 **30 分鐘**），再點擊**啟動 GPU**。實例就緒後，頂部狀態欄及概覽卡片會即時顯示計費續航、實例位置、硬件監控曲線圖及 Google 雲端硬碟掛載狀態。

4. **架設大語言模型或執行 ComfyUI**
   - **文字 API**：打開**對外 API** → **選擇模型**，從**我的**（已預置可編輯的入門 Hugging Face 倉庫）挑選 GGUF 模型，或在**社群**搜尋。點擊**下載並載入**即可在遠端下載並校驗模型分片再載入至 `llama-server`，隨後設定你的 Cloudflare Named Tunnel 並開啟 API 服務。
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
  - 在固定域名 Cloudflare Named Tunnel 後方運行具獨立令牌認證的 HTTP + WebSocket 反向代理（`127.0.0.1:8189` → `8188`）。
  - 每次工作階段均以隨機令牌換取限定作用域的 `HttpOnly` Cookie，可直接於獨立的 **ComfyUI 原生畫布** 桌面視窗或系統瀏覽器中開啟。
- **嚴格顯存互斥**
  - `llama-server`（`8081`）與 `ComfyUI`（`8188`）絕不會同時佔用 GPU 顯存。切換引擎時會自動卸載目前運行中的服務，同時完整保留遠端磁碟上的模型緩存。
  - 執行中的 ComfyUI 隊列及原生畫布操作會自動暫停閒置自動釋放倒數。

## 🔒 儲存、計費與安全

- **Google 雲端硬碟同步**：模型清單、ComfyUI 工作流與輸出作品均以受限的 `drive.file` 權限持久保存至 `My Drive / ColabOne_Drive`（ColabOne 無法讀取你雲端硬碟上其他無關的個人檔案）。模型權重存放於 Colab 的高速暫存磁碟，每次新建實例均可隨時重新下載；卸載或切換模型絕不會刪除目前實例上已緩存的檔案。
- **系統托盤與自動釋放**：關閉主視窗會將 ColabOne 縮小至系統托盤。結束程式會停止本地心跳，但遠端 VM 上的閒置自動釋放保護會繼續生效。網絡斷線不會停止 Colab 計費——請務必於**遠端裝置**確認實例已停止。
- **計費估算說明**：介面顯示的剩餘 CU 餘額、`CU/小時` 消耗速率及預計可用時長乃根據最近的 Colab 查詢結果推算，與官方結算帳單之間可能存在延遲。
- **憑證與私隱**：Google OAuth 令牌、API 金鑰、Cloudflare Tunnel Token、可選的 Hugging Face Token（用於受限倉庫）及可選的 GPU 分配代理設定（僅分配階段），均加密儲存於本地系統憑證庫，並在日誌中自動脫敏（記憶體保留最近 300 條記錄，另附本地 `0600` 權限活動日誌）。ColabOne 與現有 Colab CLI 設定檔完全隔離、互不干擾。
- **功能範圍與安全校驗**：GGUF 模型匯入會鎖定完整的 40 位 Commit SHA 並對每個分片執行 SHA-256 校驗，絕不執行倉庫內的任何 Python 代碼。舊版內置桌面對話／圖片／影片頁面已退役，由**對外 API** 及 **ComfyUI** 取代。Agent 與本地 MCP 執行不在 v0.1 範圍內。開發版本未經商業代碼簽署或公證——你隨時可以審閱源碼或在本地自行構建。

## 🔧 從源碼構建

### 📋 環境需求

- **Go** `1.27.3`
- **Node.js** `24 LTS`（或 `26`）
- **Python** `3.12`
- **`uv`**（或標準 `venv` + `pip`）

### 🔨 初始化與構建

```sh
uv venv --python 3.12
uv pip install --python .venv/bin/python -r requirements-dev.txt
npm --prefix frontend ci
cd console
go tool mygo generate
cd ..
npm --prefix frontend run build
```

### 📦 驗證與打包

> 於 Windows 平台，請將 `.venv/bin/python` 替換為 `.venv\Scripts\python.exe`。

```sh
# 執行全部離線檢查（Python、Go vet/test、Vitest、前端構建及橋接自檢）
.venv/bin/python tools/install.py --root work/check --check

# 在 dist/ 目錄打包獨立便攜壓縮包
.venv/bin/python tools/package.py --out dist
```

打包完成的免安裝便攜壓縮包會輸出至 `dist/`（未壓縮暫存目錄位於 `work/package-stage/Programs/ColabOne`），內含經雜湊校驗的獨立 CPython 執行環境與鎖定版本的 Wheel 套件，讓終端用戶無需安裝任何外部工具鏈即可直接使用。

所有單元測試與瀏覽器測試均 100% 離線使用模擬（Mock）運行——絕不會連接 Google 或 Hugging Face，亦不會申請任何 GPU 實例。真實 GPU 驗收測試會在嚴格的算力預算下明確觸發執行，詳見 [docs/validation.md](docs/validation.md)。

## 📚 技術文件

- [架構概覽](docs/architecture.md) — 本地控制橋接、遠端回環連接埠、雲端硬碟同步、硬件監控及安全邊界
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

- **[Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga)** *(Apache-2.0)* — 參考其 Google Colab GPU 分配、執行環境就緒檢測、心跳生命週期管理及免安裝便攜 Python 打包方案。
- **[EasyColab](https://github.com/wy51ai/easycolab)** *(Apache-2.0)* — 參考其桌面端與 Colab 之間的橋接架構，以及無頭 `rclone` 掛載 Google 雲端硬碟的實踐模式。
- **[colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service)** *(MIT)* — 參考其模型清單、鎖定版本的 `llama.cpp` CUDA 部署及並發分段下載策略。
- **[llama.cpp](https://github.com/ggml-org/llama.cpp)** *(MIT)* 與 **[ComfyUI](https://github.com/Comfy-Org/ComfyUI)** (`v0.46.0`, GPL-3.0) — 分別作為遠端 GGUF 大語言模型推理引擎，以及節點式圖片、影片與多模態工作流引擎整合。
- **桌面端與前端 UI 技術棧** — **[MyGo](https://github.com/egoist/mygo)**、**[Vue 3](https://github.com/vuejs/core)**、**[Vite](https://github.com/vitejs/vite)**、**[Ant Design Vue](https://github.com/vueComponent/ant-design-vue)**、**[Phosphor Icons](https://github.com/phosphor-icons/vue)**、**[Shiki](https://github.com/shikijs/shiki)**、**[flag-icons](https://github.com/lipis/flag-icons)** *(MIT)* 及 **[Sarasa Gothic](https://github.com/be5invis/Sarasa-Gothic)** *(SIL OFL 1.1)*。
- **後端、隧道與雲端硬碟依賴** — **[FastAPI](https://github.com/fastapi/fastapi)** *(MIT)*、**[Google Colab CLI](https://github.com/googlecolab/google-colab-cli)** *(Apache-2.0)*、**[cloudflared](https://github.com/cloudflare/cloudflared)** *(Apache-2.0)* 及 **[rclone](https://github.com/rclone/rclone)** *(MIT)*。

## 📜 開源授權

ColabOne 採用 **GNU General Public License v3.0**（[LICENSE](LICENSE)）開源授權，以配合遠端整合的 [ComfyUI](https://github.com/Comfy-Org/ComfyUI)（`v0.46.0`，GPL-3.0）。所有上游開源授權與版權聲明均完整保留於 [NOTICE](NOTICE) 及 [third_party/SOURCES.md](third_party/SOURCES.md)。
