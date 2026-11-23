<div align="center">
  <img src="console/resources/icon.png" width="96" height="96" alt="ColabOne" />
  <h1>ColabOne</h1>
  <p><strong>專為 Google Colab GPU 打造的輕量桌面工作區 — 遠端提供 OpenAI 相容 GGUF LLM 服務並執行 ComfyUI 工作流程。</strong></p>

  <p>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-GPL--3.0-2563eb?style=flat-square" alt="授權條款：GPL-3.0" /></a>
    <img src="https://img.shields.io/badge/Platform-macOS%20Apple%20Silicon%20%7C%20Windows%20x64-475569?style=flat-square" alt="支援平台：macOS Apple Silicon | Windows x64" />
    <img src="https://img.shields.io/badge/OpenAI%20API-Responses%20%26%20Chat-0f766e?style=flat-square" alt="OpenAI Responses 與 Chat API" />
    <img src="https://img.shields.io/badge/ComfyUI-v0.46.0-7c3aed?style=flat-square" alt="ComfyUI v0.46.0" />
  </p>

  <p>
    <a href="README.md">English</a> ·
    <a href="README.zh-CN.md">简体中文</a> ·
    <strong>繁體中文（台灣）</strong> ·
    <a href="README.zh-HK.md">繁體中文（香港）</a> ·
    <a href="README.ja-JP.md">日本語</a> ·
    <a href="README.ko-KR.md">한국어</a>
  </p>
</div>

---

ColabOne 為 **macOS (Apple Silicon)** 與 **Windows (x64)** 提供快速、原生的桌面應用程式，讓你輕鬆駕馭遠端 Google Colab GPU。無需操作繁瑣的 Notebook，即可啟動並管理多台 GPU、監控即時硬體負載與計費續航、開放專屬的 OpenAI 相容 LLM 端點，並執行 ComfyUI 工作流程。

你的電腦本機只會執行單一純 Go 原生桌面執行檔（`ColabOne.app` / `ColabOne.exe`），所有本機控制邏輯均內建於同一個行程中 —— 本機完全不需要 Python 執行環境；所有模型下載、GGUF 權重儲存、`llama.cpp` 推論與 ComfyUI 執行，全都在遠端 Colab GPU 上進行（由啟動時自動上傳的內嵌式 Python 遠端控制服務驅動）。

## ✨ 核心特色

- **多 GPU 管理與即時硬體監控（`遠端裝置`）** — 按需啟動、切換並選擇性釋放多台 Colab GPU（`T4`、`L4`、`A100`、`H100`、`G4` RTX PRO 6000），同時檢視 Google 帳戶中其他來源正在運作的執行個體。即時掌握計費狀態、`CU/小時` 消耗速率、剩餘 CU 餘額、預估可用時間、執行個體所在地區國旗，以及涵蓋 GPU VRAM／核心負載、系統記憶體、磁碟讀寫與 CPU 的 60 秒即時監控圖表。
- **智慧閒置自動釋放** — 提供可自訂的閒置自動釋放倒數（預設 **30 分鐘**，可選 `15 分鐘` 至 `2 小時`）以守護你的運算單元餘額。機制直接部署於遠端 VM，即使關閉 ColabOne 仍會持續運作，並在模型下載、載入 VRAM、對外 API 推論與 ComfyUI 執行期間自動暫停倒數。
- **OpenAI 相容 LLM 閘道（`對外 API`）** — 直接在內建的 **選擇模型** 面板瀏覽並載入 Hugging Face GGUF 模型，支援即時 VRAM 與硬碟空間相容性評估、VRAM + 系統記憶體混合載入（`--fit on`）、16／32 執行緒高速下載並自動驗證 SHA-256，以及 `8K`–`128K` 上下文長度預設。透過你自己的 Cloudflare Named Tunnel 對外提供 `/v1/models`、`/v1/chat/completions`、`/v1/responses` 與 `/v1/responses/compact` 端點。
- **ComfyUI 工作流程工坊與原生畫布（`ComfyUI`）** — 遠端執行 **ComfyUI `v0.46.0`** 並提供雙模式工作區：**工作流程工坊** 可將任何 ComfyUI API JSON 自動解析為簡潔的參數表單，支援 SHA-256 驗證預覽與匯出至本機；**原生畫布** 則透過獨立權杖驗證通道，在獨立桌面視窗或系統瀏覽器中開啟完整的節點編輯器。
- **Google 雲端硬碟自動持久保存** — 使用應用程式範圍的 `drive.file` 權限，自動將模型清單、ComfyUI 工作流程與產出作品同步至 `My Drive / ColabOne_Drive`。開箱即支援 Colab 原生 DriveFS 掛載（每次新建執行個體僅需點擊一次授權），只要同意一次免費的 Google Cloud 服務條款，即可升級為 `rclone` 永久免彈窗自動掛載。
- **為日常使用打造** — 採用純 Go 單一執行檔架構（MyGo `v0.10.0` + 行程內 Go 控制器，無需本機 Python 執行環境或同層 `runtime/`、`site-packages/` 資料夾，本機亦無需安裝 Python、Go 或 Node.js），支援 6 種介面語言、淺色／深色／跟隨系統外觀主題、本機系統鑰匙圈／憑證庫安全儲存、選用的 GPU 分配代理（僅分配階段）、一鍵熱更新遠端服務，以及自動遮蔽敏感資訊的即時執行日誌面板（`Ctrl+J`）。

## 🚀 快速開始

ColabOne 介面圍繞四個側邊欄檢視組織：**遠端裝置 → 對外 API → ComfyUI → 設定**，並於右側提供可滑出的 **執行日誌** 面板。

1. **下載並啟動**
   從 [Releases](https://github.com/luolongfei/ColabOne/releases) 下載對應平台的壓縮檔（或[從原始碼建置](#-從原始碼建置)），解壓縮後直接執行 `ColabOne.app`（macOS）或 `ColabOne.exe`（Windows）。ColabOne 是完全自包含的純 Go 單一執行檔應用程式，無需本機 Python 執行環境，也不需要同層的 `runtime/` 或 `site-packages/` 資料夾。在 Windows 上，你可以在 **設定** 中直接建立開始功能表與桌面捷徑，無需系統管理員權限。

2. **登入 Google 帳戶**
   前往 **設定** → **登入 Google**，在瀏覽器中授權你的帳戶後，ColabOne 會自動完成登入。若瀏覽器顯示授權內容頁面而非自動跳轉，請展開手動貼上區塊並點擊 **貼上並完成登入**。憑證會加密儲存在 macOS 鑰匙圈或 Windows 憑證管理員中，重啟後自動恢復並續期。

3. **啟動 GPU**
   開啟 **遠端裝置**，選擇 GPU 型號（預設為 `T4`）與閒置自動釋放時間（預設 **30 分鐘**），接著點擊 **啟動 GPU**。就緒後，頂部列與總覽卡片會即時顯示計費續航、執行個體位置、硬體監控圖表與 Google 雲端硬碟掛載狀態。

4. **啟動 LLM 服務或執行 ComfyUI**
   - **文字 API**：開啟 **對外 API** → **選擇模型**，從 **我的** 清單（預先收錄可自由編輯的入門 Hugging Face 儲存庫）挑選 GGUF 模型，或切換至 **社群** 搜尋。點擊 **下載並載入** 於遠端下載經雜湊驗證的分片檔案並載入至 `llama-server`，接著設定 Cloudflare Named Tunnel 即可開啟 API 服務。
   - **視覺與多模態工作流程**：開啟 **ComfyUI**，在 **工作流程工坊** 匯入工作流程，或在 **原生畫布** 啟動完整節點編輯器。

5. **使用完畢後釋放資源**
   在單一執行個體上點擊 **單獨停止**，或點擊 **停止全部** 勾選要終止的執行個體，未勾選的工作階段將不受影響並繼續運作。由本程式管理的執行個體在釋放前，會自動關閉公開通道並同步 `ColabOne_Drive`。

## 🌐 對外 OpenAI 相容 API

**對外 API** 整合了內建的單模型 GGUF 選擇器，以及由遠端 `llama-server`（`127.0.0.1:8081`）驅動、採 Bearer 權杖驗證的專屬 OpenAI 閘道（`127.0.0.1:7861`）。管理路由（`/api/v1/*`）隔離在內部控制連接埠上，絕不會暴露至公開網路。

### 📡 支援端點

| 方法 | 端點 | 說明 |
|---|---|---|
| `GET` | `/v1/models` `/v1/models/{id}` | 列出目前載入於遠端 VRAM 的 GGUF 模型（含目前生效的上下文長度）。 |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions 對話補全，支援 `stream=true/false`、工具呼叫與 `developer` 角色。 |
| `POST` | `/v1/responses` | OpenAI Responses API（`wire_api = "responses"`），支援 SSE 串流、function 與 custom／`apply_patch` 工具、推理摘要，以及自動三階段上下文壓縮。 |
| `POST` | `/v1/responses/compact` | 適用於長篇程式開發對話的 Codex / OpenAI 上下文壓縮端點（`response.compaction`）。 |

### 🔌 連線外部用戶端

1. **設定固定網域通道**：在 Cloudflare Zero Trust 中建立一個 Cloudflared Named Tunnel，並將你的公開 HTTPS 主機名稱對應至 `http://127.0.0.1:7861`（請勿為該 API 主機名稱開啟 Cloudflare 互動式 Access 登入驗證）。將安裝指令或 Tunnel Token 貼上至 **對外 API**，儲存後點擊 **開啟 API 服務**。（*程式刻意停用臨時 `trycloudflare.com` 網域。*）
2. **將應用程式或 SDK 指向 ColabOne**：複製 **Base URL**、**API Key** 與 **目前模型 ID**（可即時編輯，無需重新載入 VRAM；亦可使用固定別名 `colabone/auto` 自動路由至目前載入的模型）。應用程式內建 **cURL**、**Python** 與 **Codex** 呼叫範例，並即時顯示每次請求的 Token 速度（`tok/s`、首字延遲 TTFT、提示詞預填充速度）。

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="https://api.example.com/v1",
    api_key=os.environ["COLABONE_API_KEY"],
)

stream = client.responses.create(
    model="colabone/auto",
    input="Hello from ColabOne!",
    stream=True,
)
for event in stream:
    if event.type == "response.output_text.delta":
        print(event.delta, end="", flush=True)
```

關閉 **對外 API** 會立即撤銷外部存取權限，同時保留遠端 GPU 與已載入的模型於待機狀態；停止 GPU 則會自動關閉閘道與通道。完整的端點介面規格與錯誤碼請參閱 [docs/api.md](docs/api.md#public-inference-api-llm-openai-gateway)。

## 🎨 ComfyUI 工作區

ColabOne 僅於遠端 Colab GPU（`127.0.0.1:8188`）部署 **ComfyUI `v0.46.0`**，並提供雙層工作區體驗：

- **工作流程工坊**
  - 支援匯入任何 ComfyUI **Save (API Format)** JSON 工作流程 — 即使尚未啟動 GPU 也能離線管理 — 並自動產生具型別的參數表單。
  - 上傳參考圖片或音訊、送出可隨時取消的遠端佇列工作，並即時追蹤節點執行進度。
  - 線上預覽經 SHA-256 驗證的圖片、影片與音訊作品，並透過原生儲存對話框匯出至本機；工作流程（`/workflows/**`）與輸出作品（`/comfyui-outputs/**`）會自動同步至 `ColabOne_Drive`。
  - 透過 `catalog/comfyui.json` 以宣告式方式管理遠端模型與工作流程套件（純資料清單，鎖定修訂版本並執行 SHA-256 驗證）。
- **原生畫布**
  - 在你的固定網域 Cloudflare Named Tunnel 後方執行採權杖驗證的 HTTP + WebSocket 反向代理（`127.0.0.1:8189` → `8188`）。
  - 每次工作階段以隨機權杖換取限定範圍的 `HttpOnly` Cookie，可直接於獨立的 **ComfyUI 原生畫布** 應用程式視窗或系統瀏覽器中開啟。
- **嚴格 VRAM 互斥**
  - `llama-server`（`8081`）與 `ComfyUI`（`8188`）絕不會同時佔用 GPU VRAM。切換引擎時會自動卸載運作中的引擎，並保留快取於遠端磁碟的模型權重。
  - 運作中的 ComfyUI 執行佇列與原生畫布操作皆會自動暫停閒置自動釋放倒數。

## 🔒 儲存、計費與安全性

- **Google 雲端硬碟同步**：模型清單、ComfyUI 工作流程與輸出作品會以受限的 `drive.file` 權限範圍持久保存至 `My Drive / ColabOne_Drive`（ColabOne 無法存取其他無關的個人雲端硬碟檔案）。模型權重存放於 Colab 的高速暫存磁碟，每次啟動新 VM 時可重新下載；在運作中的執行個體上卸載或切換模型，絕不會刪除已快取的檔案。
- **系統匣與自動釋放**：關閉視窗會將 ColabOne 最小化至系統匣。結束應用程式會停止本機心跳回報，但遠端 VM 上的閒置自動釋放保護機制仍會持續運作。網路斷線並不會停止 Colab 計費 — 請務必在 **遠端裝置** 確認執行個體已停止。
- **計費估算說明**：介面顯示的剩餘 CU 餘額、`CU/小時` 消耗速率與預估可用時間皆依據近期的 Colab 查詢結果計算，可能與官方帳單略有時間差。
- **憑證與隱私保護**：Google OAuth 權杖、API 金鑰、Cloudflare Tunnel Token、選用的 Hugging Face Token（供存取受限儲存庫）以及選用的 GPU 分配代理（僅分配階段）設定，均儲存在本機系統鑰匙圈／憑證庫中，並於日誌中自動遮蔽（記憶體保留最近 300 筆，另寫入權限為 `0600` 的本機活動日誌）。ColabOne 與既有的 Colab CLI 設定檔完全獨立。
- **功能範圍與安全驗證**：匯入 GGUF 模型時會鎖定完整的 40 字元 commit SHA，並對每個分片進行 SHA-256 驗證，全程不執行儲存庫內的 Python 程式碼。舊版內建桌面對話／圖片／影片頁面已下線，改由 **對外 API** 與 **ComfyUI** 取代。Agent 與本機 MCP 執行不在 v0.1 範圍內。開發版本未進行商業程式碼簽章或公證 — 歡迎隨時檢視原始碼或於本機自行建置。

## 🔧 從原始碼建置

### 📋 環境需求

- **Go** `1.27.3`
- **Node.js** `24 LTS`（或 `26`）
- **Python** `3.12` 與 **`uv`**（或標準 `venv` + `pip`）—— 僅用於建置與測試階段（同步內嵌遠端服務包、執行遠端 Python 單元測試與封裝腳本）

### 🔨 安裝與建置

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

### 📦 驗證與封裝

> 在 Windows 上，請將 `.venv/bin/python` 替換為 `.venv\Scripts\python.exe`。

```sh
# 執行所有離線檢查（內嵌服務包同步驗證、Go vet/test、遠端 Python pytest、Vitest 與前端建置）
.venv/bin/python tools/install.py --root work/check --check

# 於 dist/ 建置純 Go 單一執行檔可攜版壓縮檔
.venv/bin/python tools/package.py --out dist
```

可攜版壓縮檔會輸出至 `dist/`（未壓縮的暫存組裝目錄位於 `work/package-stage/Programs/ColabOne`），封裝獨立的純 Go 單一執行檔（`ColabOne.app` / `ColabOne.exe`），並透過 `go:embed` 直接內嵌編譯後的 Vue 前端資源與遠端 Python 控制服務（`server/`、`common/`、`catalog/`），終端使用者無需本機 Python 執行環境或任何同層相依資料夾。

所有單元測試與瀏覽器測試皆 100% 離線以模擬資料（Mock）執行 — 絕不會連線至 Google 或 Hugging Face，也不會配置真實 GPU。真實 GPU 驗收測試會在嚴格的運算預算下另行手動執行，詳情請參閱 [docs/validation.md](docs/validation.md)。

## 📚 相關文件

- [架構總覽](docs/architecture.md) — 純 Go 行程內控制器、內嵌遠端服務、loopback 連接埠、雲端硬碟同步、即時遙測與安全邊界
- [遠端與對外 API 參考文件](docs/api.md) — 控制端點路由、OpenAI 閘道介面規格與 ComfyUI 端點
- [驗證紀錄](docs/validation.md) — 離線 CI 測試涵蓋範圍與真實 GPU 驗收紀錄
- [上游來源與授權歸屬](third_party/SOURCES.md) — 鎖定的第三方相依項目與授權宣告

## 🤝 參與貢獻

非常歡迎任何程式碼貢獻、錯誤回報與功能建議！本機開發環境建置、PR 規範與離線測試要求，請參閱[貢獻指南](CONTRIBUTING.md)。

### 💖 貢獻者

感謝每一位協助開發與改進 ColabOne 的貢獻者：

<a href="https://github.com/luolongfei/ColabOne/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=luolongfei/ColabOne" alt="ColabOne 貢獻者" />
</a>

## 🎉 致謝

特別感謝 **[Alma](https://alma.now/)** 與 **[Antigravity-Manager](https://github.com/lbjlaq/Antigravity-Manager)** — ColabOne 的介面設計、視覺質感乃至應用程式 Logo，皆深受這兩個專案的啟發與參考。

同時衷心感謝以下開源專案，正是因為有它們的開源分享與底層支撐，才有了今天的 ColabOne（完整資訊請參閱 [NOTICE](NOTICE) 與 [third_party/SOURCES.md](third_party/SOURCES.md)）：

- **[Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga)** *(Apache-2.0)* — 參考了 Google Colab GPU 分配、執行環境就緒檢測與心跳生命週期管理設計。
- **[EasyColab](https://github.com/wy51ai/easycolab)** *(Apache-2.0)* — 參考了桌面端與 Colab 之間的控制架構，以及免彈窗 `rclone` Google 雲端硬碟掛載模式。
- **[colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service)** *(MIT)* — 參考了模型清單設計、鎖定版本的 `llama.cpp` CUDA 部署與並行分段下載策略。
- **[llama.cpp](https://github.com/ggml-org/llama.cpp)** *(MIT)* 與 **[ComfyUI](https://github.com/Comfy-Org/ComfyUI)** (`v0.46.0`, GPL-3.0) — 分別整合為遠端 GGUF LLM 推論引擎與節點式圖片、影片及多模態工作流程引擎。
- **桌面端與前端 UI 技術棧** — **[MyGo](https://github.com/egoist/mygo)** (`v0.10.0`, MIT)、**[coder/websocket](https://github.com/coder/websocket)** *(ISC)*、**[Vue 3](https://github.com/vuejs/core)**、**[Vite](https://github.com/vitejs/vite)**、**[Ant Design Vue](https://github.com/vueComponent/ant-design-vue)**、**[Phosphor Icons](https://github.com/phosphor-icons/vue)**、**[Shiki](https://github.com/shikijs/shiki)**、**[flag-icons](https://github.com/lipis/flag-icons)** *(MIT)*，以及 **[Sarasa Gothic](https://github.com/be5invis/Sarasa-Gothic)** *(SIL OFL 1.1)*。
- **遠端服務、通道與雲端硬碟相依項目** — **[FastAPI](https://github.com/fastapi/fastapi)** *(MIT)*、**[Google Colab CLI](https://github.com/googlecolab/google-colab-cli)** *(Apache-2.0)*、**[cloudflared](https://github.com/cloudflare/cloudflared)** *(Apache-2.0)* 與 **[rclone](https://github.com/rclone/rclone)** *(MIT)*。

## 📜 授權條款

ColabOne 採用 **GNU General Public License v3.0**（[LICENSE](LICENSE)）授權條款，與遠端整合的 [ComfyUI](https://github.com/Comfy-Org/ComfyUI)（`v0.46.0`，GPL-3.0）保持一致。所有上游授權與著作權聲明皆完整保留於 [NOTICE](NOTICE) 與 [third_party/SOURCES.md](third_party/SOURCES.md)。
