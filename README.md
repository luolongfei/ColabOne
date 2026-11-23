<div align="center">
  <img src="console/resources/icon.png" width="96" height="96" alt="ColabOne" />
  <h1>ColabOne</h1>
  <p><strong>A lightweight desktop workspace for Google Colab GPUs — serve OpenAI-compatible GGUF LLMs and run ComfyUI workflows remotely.</strong></p>

  <p>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-GPL--3.0-2563eb?style=flat-square" alt="License: GPL-3.0" /></a>
    <img src="https://img.shields.io/badge/Platform-macOS%20Apple%20Silicon%20%7C%20Windows%20x64-475569?style=flat-square" alt="Platform: macOS Apple Silicon | Windows x64" />
    <img src="https://img.shields.io/badge/OpenAI%20API-Responses%20%26%20Chat-0f766e?style=flat-square" alt="OpenAI Responses & Chat API" />
    <img src="https://img.shields.io/badge/ComfyUI-v0.46.0-7c3aed?style=flat-square" alt="ComfyUI v0.46.0" />
  </p>

  <p>
    <strong>English</strong> ·
    <a href="README.zh-CN.md">简体中文</a> ·
    <a href="README.zh-TW.md">繁體中文（台灣）</a> ·
    <a href="README.zh-HK.md">繁體中文（香港）</a> ·
    <a href="README.ja-JP.md">日本語</a> ·
    <a href="README.ko-KR.md">한국어</a>
  </p>
</div>

---

ColabOne puts your remote Google Colab GPUs behind a fast, native desktop app for **macOS (Apple Silicon)** and **Windows (x64)**. Spin up and manage multiple GPUs, monitor live hardware load and billing endurance, expose a private OpenAI-compatible LLM endpoint, and run ComfyUI workflows — with zero notebook wrangling.

Your computer only runs a single pure Go native desktop binary (`ColabOne.app` / `ColabOne.exe`) with an embedded in-process controller — zero local Python runtime required. All model downloads, GGUF weights, `llama.cpp` inference, and ComfyUI execution happen exclusively on the remote Colab GPU (powered by the embedded Python control service uploaded automatically on startup).

## ✨ Highlights

- **Multi-GPU Management & Live Telemetry (`Devices`)** — Launch, switch between, and selectively release multiple Colab GPUs (`T4`, `L4`, `A100`, `H100`, `G4` RTX PRO 6000), while also inspecting external instances running on your Google account. Track live billing status, `CU/hr` burn rate, remaining CU balance, estimated runtime, region flags, and 60-second charts for GPU VRAM/utilization, RAM, disk I/O, and CPU.
- **Smart Idle Auto-Release** — Protects your compute balance with a configurable idle countdown (default **30 min**; options from `15 min` to `2 hr`). Enforced on the remote VM so it keeps working even if you quit ColabOne, and automatically pauses during downloads, VRAM loads, API inference, and ComfyUI activity.
- **OpenAI-Compatible LLM Gateway (`Public API`)** — Browse and load Hugging Face GGUF models directly from the built-in **Select Model** drawer with real-time VRAM & disk fit checks, automatic VRAM + RAM hybrid loading (`--fit on`), 16/32-thread SHA-256-verified downloads, and `8K`–`128K` context presets. Expose `/v1/models`, `/v1/chat/completions`, `/v1/responses`, and `/v1/responses/compact` over your own Cloudflare Named Tunnel.
- **ComfyUI Studio & Native Canvas (`ComfyUI`)** — Run remote **ComfyUI `v0.46.0`** in two modes: **Workflow Studio** turns any ComfyUI API JSON into a clean parameter form with SHA-256-verified previews and local export, while **Native Canvas** opens the full node editor in a dedicated app window or browser over a token-authenticated tunnel.
- **Automatic Google Drive Persistence** — Saves your model catalog, ComfyUI workflows, and generated outputs to `My Drive / ColabOne_Drive` using app-scoped `drive.file` permissions. Works out of the box with Colab native DriveFS (one click per new VM) and upgrades to permanent zero-prompt headless `rclone` auto-mount once you accept the free Google Cloud Terms of Service.
- **Built for Everyday Use** — Ships as a pure Go single-binary desktop app (MyGo `v0.10.0` + in-process Go controller; zero local Python runtime or sibling `runtime/`/`site-packages/` folders needed, no local Python, Go, or Node required), with 6 UI languages, Light/Dark/System themes, OS keychain secret storage, an optional assign-only GPU allocation proxy, one-click remote service hot-updates, and a live redacted log drawer (`Ctrl+J`).

## 🚀 Quick Start

ColabOne is organized around four sidebar views: **Devices → Public API → ComfyUI → Settings**, plus a slide-over **Logs** drawer on the right.

1. **Download & Launch**
   Download the archive for your platform from [Releases](https://github.com/luolongfei/ColabOne/releases) (or [build from source](#-building-from-source)), extract it, and open `ColabOne.app` (macOS) or `ColabOne.exe` (Windows). ColabOne is a self-contained pure Go single-binary app — no local Python runtime or sibling `runtime/`/`site-packages/` folders are required. On Windows, you can create Start Menu and Desktop shortcuts from **Settings** without admin rights.

2. **Sign In with Google**
   Go to **Settings** → **Sign in with Google**, authorize your account in the browser, and ColabOne will complete login automatically. If your browser displays an authorization page instead of redirecting, expand the manual paste section and click **Paste & Sign In**. Credentials stay encrypted in macOS Keychain or Windows Credential Manager and auto-renew across restarts.

3. **Launch a GPU**
   Open **Devices**, select a GPU tier (defaults to `T4`) and idle auto-release timeout (default **30 min**), and click **Start GPU**. Once ready, the top bar and overview card display live billing endurance, instance location, hardware telemetry charts, and Google Drive mount status.

4. **Serve an LLM or Run ComfyUI**
   - **Text API**: Open **Public API** → **Select Model** to pick a GGUF model from **My Models** (pre-seeded with editable starter Hugging Face repos) or search under **Community**. Click **Download & Load** to fetch verified shards remotely and load them into `llama-server`, then configure your Cloudflare Named Tunnel and start the API.
   - **Visual & Multimodal Workflows**: Open **ComfyUI** to import a workflow in **Workflow Studio** or launch the full node editor in **Native Canvas**.

5. **Release When Finished**
   Click **Stop** on an individual instance, or click **Stop All** to selectively check which instances to terminate while leaving other sessions untouched. Managed instances automatically close public tunnels and flush `ColabOne_Drive` before releasing.

## 🌐 Public OpenAI-Compatible API

**Public API** pairs a built-in single-model GGUF picker with a dedicated, Bearer-authenticated OpenAI gateway (`127.0.0.1:7861`) backed by remote `llama-server` (`127.0.0.1:8081`). Management routes (`/api/v1/*`) stay isolated on a private control port and are never exposed publicly.

### 📡 Supported Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/v1/models` `/v1/models/{id}` | Lists the GGUF model currently loaded in remote VRAM (with active context length). |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions with `stream=true/false`, tool calling, and `developer` role support. |
| `POST` | `/v1/responses` | OpenAI Responses API (`wire_api = "responses"`) with SSE streaming, function & custom/`apply_patch` tools, reasoning summaries, and automatic 3-stage context compaction. |
| `POST` | `/v1/responses/compact` | Codex / OpenAI context compaction (`response.compaction`) for long coding threads. |

### 🔌 Connecting External Clients

1. **Configure a Fixed-Domain Tunnel**: In Cloudflare Zero Trust, create a Cloudflared Named Tunnel and map your public HTTPS hostname to `http://127.0.0.1:7861` (do not enable interactive Cloudflare Access login on the API hostname). Paste the install command or tunnel token into **Public API**, save, and click **Start Public API**. *(Temporary `trycloudflare.com` domains are intentionally disabled.)*
2. **Point Your App or SDK to ColabOne**: Copy the **Base URL**, **API Key**, and **Active Model ID** (editable live without reloading VRAM — or use the fixed alias `colabone/auto` to route to whichever model is loaded). Built-in **cURL**, **Python**, and **Codex** snippets and live per-request token throughput (`tok/s`, TTFT, prefill speed) are shown right inside the app.

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

Stopping **Public API** revokes public access while keeping your GPU and loaded model warm; stopping the GPU automatically shuts down the gateway and tunnel. See [docs/api.md](docs/api.md#public-inference-api-llm-openai-gateway) for full route contracts and error codes.

## 🎨 ComfyUI Workspace

ColabOne deploys **ComfyUI `v0.46.0`** exclusively on the remote Colab GPU (`127.0.0.1:8188`) with a dual-layer workspace:

- **Workflow Studio**
  - Import any ComfyUI **Save (API Format)** JSON workflow — even offline before starting a GPU — and auto-generate typed parameter forms.
  - Upload reference images or audio, submit cancellable remote queue runs, and track live node progress.
  - Preview and export SHA-256-verified image, video, and audio outputs via native save dialogs; workflows (`/workflows/**`) and outputs (`/comfyui-outputs/**`) sync automatically to `ColabOne_Drive`.
  - Manage remote model and workflow packs declaratively through `catalog/comfyui.json` (data-only manifests with pinned revisions and SHA-256 checks).
- **Native Canvas**
  - Runs a token-authenticated HTTP + WebSocket reverse proxy (`127.0.0.1:8189` → `8188`) behind your fixed-domain Cloudflare Named Tunnel.
  - Exchanges a random per-session token for a scoped `HttpOnly` cookie and opens directly in a dedicated **ComfyUI Native Canvas** app window or your system browser.
- **Strict VRAM Mutual Exclusion**
  - `llama-server` (`8081`) and `ComfyUI` (`8188`) never occupy GPU VRAM at the same time. Switching automatically unloads the active engine while retaining cached weights on remote disk.
  - Active ComfyUI queues and Native Canvas requests automatically pause the idle auto-release countdown.

## 🔒 Storage, Billing & Security

- **Google Drive Sync**: Model catalogs, ComfyUI workflows, and outputs persist to `My Drive / ColabOne_Drive` under the restricted `drive.file` scope (ColabOne cannot read unrelated personal Drive files). Model weights live on Colab's fast ephemeral disk and can be re-downloaded whenever you start a new VM. Unloading or switching models never deletes cached files on the active instance.
- **System Tray & Auto-Release**: Closing the window minimizes ColabOne to the tray. Quitting stops local heartbeats, while the remote idle auto-release guard continues running on the VM. A dropped connection does not stop Colab billing — always confirm stopped status in **Devices**.
- **Billing Estimates**: Displayed CU balance, `CU/hr` burn rate, and estimated remaining runtime are based on recent Colab queries and may lag official billing statements.
- **Credentials & Privacy**: Google OAuth tokens, API keys, Cloudflare Tunnel tokens, optional Hugging Face tokens (for gated repos), and optional assign-only GPU allocation proxy settings are stored in your OS keychain and redacted from logs (last 300 entries in memory plus a local `0600` activity log). ColabOne stays completely independent of existing Colab CLI profiles.
- **Scope & Verification**: GGUF imports pin exact 40-character commit SHAs and verify every shard with SHA-256 without executing repository Python code. Legacy built-in desktop chat/image/video pages have been retired in favor of **Public API** and **ComfyUI**. Agent and local MCP execution are out of scope for v0.1. Development builds are not commercially code-signed or notarized — inspect the source or build locally at any time.

## 🔧 Building from Source

### 📋 Prerequisites

- **Go** `1.27.3`
- **Node.js** `24 LTS` (or `26`)
- **Python** `3.12` + **`uv`** (or standard `venv` + `pip`) — used only at build/test time to sync the embedded remote service bundle, run remote-service pytest suites, and execute packaging scripts

### 🔨 Setup & Build

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

### 📦 Verify & Package

> On Windows, replace `.venv/bin/python` with `.venv\Scripts\python.exe`.

```sh
# Run all offline checks (embedded service sync, Go vet/test, remote Python pytest, Vitest, frontend build)
.venv/bin/python tools/install.py --root work/check --check

# Build a self-contained pure Go single-binary archive in dist/
.venv/bin/python tools/package.py --out dist
```

Portable archives are written to `dist/` (with the unpacked staging directory at `work/package-stage/Programs/ColabOne`), shipping the standalone pure Go binary (`ColabOne.app` / `ColabOne.exe`) with the compiled Vue frontend and remote Python control service (`server/`, `common/`, `catalog/`) embedded via `go:embed` — zero local Python runtime or sibling dependency folders required.

All unit and browser tests run 100% offline with mocks — they never contact Google or Hugging Face and never provision a GPU. Real-GPU acceptance is run explicitly on a strict compute budget; see [docs/validation.md](docs/validation.md).

## 📚 Documentation

- [Architecture Overview](docs/architecture.md) — Pure Go in-process controller, embedded remote service, loopback ports, Drive sync, telemetry, and security boundaries
- [Remote & Public API Reference](docs/api.md) — Control routes, OpenAI gateway contract, and ComfyUI endpoints
- [Validation Record](docs/validation.md) — Offline CI coverage and real-GPU acceptance logs
- [Upstream Provenance](third_party/SOURCES.md) — Pinned third-party dependencies and attributions

## 🤝 Contributing

Contributions, bug reports, and feature suggestions are always welcome! See the [Contributing Guide](CONTRIBUTING.md) for local setup, PR guidelines, and offline testing requirements.

### 💖 Contributors

Thanks to everyone who has helped build and improve ColabOne:

<a href="https://github.com/luolongfei/ColabOne/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=luolongfei/ColabOne" alt="ColabOne Contributors" />
</a>

## 🎉 Acknowledgments

Special thanks to **[Alma](https://alma.now/)** and **[Antigravity-Manager](https://github.com/lbjlaq/Antigravity-Manager)** — ColabOne's UI design, visual polish, and even its logo were directly inspired by their work.

Heartfelt thanks as well to the open-source projects below — ColabOne simply wouldn't exist without their open work and the foundation they provide (full details in [NOTICE](NOTICE) and [third_party/SOURCES.md](third_party/SOURCES.md)):

- **[Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga)** *(Apache-2.0)* — Inspired our Google Colab GPU allocation, runtime readiness, and heartbeat lifecycle.
- **[EasyColab](https://github.com/wy51ai/easycolab)** *(Apache-2.0)* — Inspired our desktop-to-Colab control architecture and headless `rclone` Google Drive mounting.
- **[colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service)** *(MIT)* — Inspired our model manifests, pinned `llama.cpp` CUDA deployment, and parallel range downloads.
- **[llama.cpp](https://github.com/ggml-org/llama.cpp)** *(MIT)* & **[ComfyUI](https://github.com/Comfy-Org/ComfyUI)** (`v0.46.0`, GPL-3.0) — Remote GGUF LLM inference engine and node-based image, video, and multimodal workflow engine.
- **Desktop & UI stack** — **[MyGo](https://github.com/egoist/mygo)** (`v0.10.0`, MIT), **[coder/websocket](https://github.com/coder/websocket)** *(ISC)*, **[Vue 3](https://github.com/vuejs/core)**, **[Vite](https://github.com/vitejs/vite)**, **[Ant Design Vue](https://github.com/vueComponent/ant-design-vue)**, **[Phosphor Icons](https://github.com/phosphor-icons/vue)**, **[Shiki](https://github.com/shikijs/shiki)**, **[flag-icons](https://github.com/lipis/flag-icons)** *(MIT)*, and **[Sarasa Gothic](https://github.com/be5invis/Sarasa-Gothic)** *(SIL OFL 1.1)*.
- **Remote service, tunnel & Drive stack** — **[FastAPI](https://github.com/fastapi/fastapi)** *(MIT)*, **[Google Colab CLI](https://github.com/googlecolab/google-colab-cli)** *(Apache-2.0)*, **[cloudflared](https://github.com/cloudflare/cloudflared)** *(Apache-2.0)*, and **[rclone](https://github.com/rclone/rclone)** *(MIT)*.

## 📜 License

ColabOne is licensed under the **GNU General Public License v3.0** ([LICENSE](LICENSE)) to align with remote-integrated [ComfyUI](https://github.com/Comfy-Org/ComfyUI) (`v0.46.0`, GPL-3.0). All upstream license and copyright notices are preserved in [NOTICE](NOTICE) and [third_party/SOURCES.md](third_party/SOURCES.md).
