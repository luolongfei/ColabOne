<div align="center">
  <img src="console/resources/icon.png" width="96" height="96" alt="ColabOne" />
  <h1>ColabOne</h1>
  <p><strong>轻量级 Google Colab GPU 桌面工作台 —— 在远端提供 OpenAI 兼容的 GGUF 大模型服务并运行 ComfyUI 工作流。</strong></p>

  <p>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-GPL--3.0-2563eb?style=flat-square" alt="许可证：GPL-3.0" /></a>
    <img src="https://img.shields.io/badge/Platform-macOS%20Apple%20Silicon%20%7C%20Windows%20x64-475569?style=flat-square" alt="平台：macOS Apple Silicon | Windows x64" />
    <img src="https://img.shields.io/badge/OpenAI%20API-Responses%20%26%20Chat-0f766e?style=flat-square" alt="OpenAI Responses 与 Chat API" />
    <img src="https://img.shields.io/badge/ComfyUI-v0.46.0-7c3aed?style=flat-square" alt="ComfyUI v0.46.0" />
  </p>

  <p>
    <a href="README.md">English</a> ·
    <strong>简体中文</strong> ·
    <a href="README.zh-TW.md">繁體中文（台灣）</a> ·
    <a href="README.zh-HK.md">繁體中文（香港）</a> ·
    <a href="README.ja-JP.md">日本語</a> ·
    <a href="README.ko-KR.md">한국어</a>
  </p>
</div>

---

ColabOne 是一款面向 **macOS (Apple Silicon)** 与 **Windows (x64)** 的原生轻量级桌面应用，可将你的远端 Google Colab GPU 统一纳入本地工作台。无需折腾 Notebook，即可按需启动和管理多台 GPU、实时监控硬件负载与算力计费续航、对外提供私有 OpenAI 兼容大模型接口，并运行 ComfyUI 工作流。

你的本机仅运行单个纯 Go 原生桌面可执行程序（`ColabOne.app` / `ColabOne.exe`），所有本地控制逻辑均内嵌于进程内 —— 无需任何本机 Python 运行时。所有模型下载、GGUF 权重缓存、`llama.cpp` 推理以及 ComfyUI 执行均完全在远端 Colab GPU 上完成（由启动时自动上传的嵌入式 Python 远端控制服务驱动）。

## ✨ 核心特性

- **多 GPU 管理与实时硬件监控（`远端设备`）** — 按需启动、切换并选择性释放多台 Colab GPU（`T4`、`L4`、`A100`、`H100`、`G4` RTX PRO 6000），同时可查看同一 Google 账户下运行的其他来源实例。实时追踪计费状态、`CU/小时` 消耗速率、剩余 CU 余额、预计可用时长、实例地区国旗标识，以及 GPU 显存与核心负载、系统内存、磁盘 I/O 和处理器的 60 秒实时曲线。
- **智能空闲自动释放** — 通过可配置的空闲倒计时守护算力余额（默认 **30 分钟**，支持 `15 分钟` 至 `2 小时`）。策略在远端虚拟机内强制生效，即使退出 ColabOne 也能继续保护；模型下载、显存装载、对外 API 推理及 ComfyUI 运行期间会自动暂停倒计时。
- **OpenAI 兼容大模型网关（`对外 API`）** — 直接在内置**选择模型**抽屉中浏览并装载 Hugging Face GGUF 模型，支持实时显存与磁盘适配校验、显存 + 系统内存混合装载（`--fit on`）、16/32 线程 SHA-256 校验高速下载，以及 `8K`–`128K` 上下文档位。通过你自己的 Cloudflare Named Tunnel 对外提供 `/v1/models`、`/v1/chat/completions`、`/v1/responses` 与 `/v1/responses/compact` 接口。
- **ComfyUI 工坊与原生画布（`ComfyUI`）** — 以双模式在远端运行 **ComfyUI `v0.46.0`**：**工作流工坊**可将任意 ComfyUI API JSON 自动解析为简洁的参数表单，支持经 SHA-256 校验的作品预览与导出到本地；**原生画布**则通过令牌鉴权隧道，在独立桌面窗口或系统浏览器中打开完整节点编辑器。
- **Google 云盘自动持久化** — 使用应用专属的 `drive.file` 权限，将模型清单、ComfyUI 工作流与输出作品自动保存至 `My Drive / ColabOne_Drive`。开箱即支持 Colab 原生 DriveFS（每次新建实例点击授权一次），同意一次免费的 Google Cloud 服务条款后即可升级为永久免弹窗的无头 `rclone` 自动挂载。
- **面向日常使用打磨** — 纯 Go 单文件原生桌面应用（MyGo `v0.10.0` + 进程内 Go 控制器，无需本机 Python 运行时或同级 `runtime/`、`site-packages/` 目录，也无需预装 Python、Go 或 Node），提供 6 种界面语言、浅色 / 深色 / 跟随系统主题、系统凭据库密钥加密存储、可选的仅分配阶段 GPU 分配代理、一键应用内自动更新（基于 GitHub Releases，支持 Ed25519 签名校验、增量补丁及可选预发布通道）、一键热更新远端服务，以及自动脱敏的实时**运行日志**抽屉（`Ctrl+J`）。

## 🚀 快速开始

ColabOne 围绕四个侧边栏视图组织：**远端设备 → 对外 API → ComfyUI → 设置**，并在右侧提供可滑出展开的**运行日志**抽屉。

1. **下载并启动**
   从 [Releases](https://github.com/luolongfei/ColabOne/releases) 下载对应平台的压缩包（或[从源码构建](#-从源码构建)），解压后直接打开 `ColabOne.app`（macOS）或 `ColabOne.exe`（Windows）。ColabOne 是完全自包含的纯 Go 单文件桌面应用，无需本机 Python 运行时，也无需保留 `runtime/`、`site-packages/` 等同级目录。在 Windows 上，无需管理员权限即可在**设置**中创建开始菜单与桌面快捷方式。

2. **登录 Google 账户**
   前往**设置** → **登录 Google**，在浏览器中完成账户授权，ColabOne 会自动完成登录。如果浏览器停留在授权页面而未自动跳转，请展开手动粘贴区域并点击**粘贴并完成登录**。凭据会加密保存在 macOS 钥匙串或 Windows 凭据管理器中，重启自动恢复并到期自动续期。

3. **启动 GPU**
   打开**远端设备**，选择 GPU 型号（默认 `G4`）与空闲自动释放时长（默认 **30 分钟**），然后点击**启动 GPU**。就绪后，顶部状态栏与概览卡片会实时展示算力计费续航、实例位置、实时硬件监控曲线以及 Google 云盘挂载状态。

4. **部署大模型或运行 ComfyUI**
   - **文本 API**：打开**对外 API** → **选择模型**，从**我的**（已预置可编辑的入门 Hugging Face 仓库）中挑选 GGUF 模型，或在**社区**中搜索。点击**下载并装载**，客户端会在远端高速拉取并校验分片后装载至 `llama-server`；随后配置你的 Cloudflare Named Tunnel 并开启 API 服务即可。
   - **视觉与多模态工作流**：打开 **ComfyUI**，在**工作流工坊**中导入工作流，或在**原生画布**中启动完整节点编辑器。

5. **用完即释放**
   在单个实例上点击**单独停止**，或点击**停止全部**并自由勾选需要终止的实例，其余运行中的会话不受影响。由本客户端管理的实例在释放前会自动关闭公网隧道，并将数据刷新同步至 `ColabOne_Drive`。

## 🌐 对外 OpenAI 兼容 API

**对外 API** 将内置的单模型 GGUF 选择器与独立的 Bearer 鉴权 OpenAI 网关（`127.0.0.1:7861`）相结合，底层由远端 `llama-server`（`127.0.0.1:8081`）提供推理。所有管理路由（`/api/v1/*`）均隔离在私有控制端口中，绝不暴露到公网。

### 📡 支持的接口

| 方法 | 接口路径 | 说明 |
|---|---|---|
| `GET` | `/v1/models` `/v1/models/{id}` | 返回当前已装载到远端显存中的 GGUF 模型及其生效的上下文长度。 |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions 对话补全接口，支持 `stream=true/false`、工具调用以及 `developer` 角色。 |
| `POST` | `/v1/responses` | OpenAI Responses API（`wire_api = "responses"`），支持 SSE 流式输出、函数工具与自定义 / `apply_patch` 工具、推理摘要，以及自动三阶段上下文压缩。 |
| `POST` | `/v1/responses/compact` | 面向长编程会话的 Codex / OpenAI 上下文压缩接口（`response.compaction`）。 |

### 🔌 接入外部客户端

1. **配置固定域名隧道**：在 Cloudflare Zero Trust 中创建一个 Cloudflared Named Tunnel，将你的公网 HTTPS 域名映射至 `http://127.0.0.1:7861`（请勿为 API 域名开启 Cloudflare 交互式 Access 登录验证）。将安装命令或 Tunnel Token 粘贴到**对外 API** 页面中，点击**保存配置**，再点击**开启 API 服务**。（*临时 `trycloudflare.com` 域名已被主动禁用*）
2. **将应用或 SDK 指向 ColabOne**：复制页面中显示的 **Base URL**、**API Key** 与**当前模型 ID**（支持在不重载显存的情况下实时自定义修改，也可直接使用固定别名 `colabone/auto` 自动路由到当前装载的模型）。应用内直接提供 **cURL**、**Python** 与 **Codex** 调用示例，并实时展示每次外部请求的 Token 生成速度（`tok/s`、首字延迟 TTFT 与预填充速度）。

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

关闭**对外 API** 仅撤销公网访问权限，不会释放 GPU 或卸载已装载的模型；停止 GPU 实例时则会自动关闭网关与隧道。完整接口契约与错误码请参阅 [docs/api.md](docs/api.md#public-inference-api-llm-openai-gateway)。

## 🎨 ComfyUI 工作区

ColabOne 将 **ComfyUI `v0.46.0`** 完全部署在远端 Colab GPU（`127.0.0.1:8188`）上，并提供双层工作区：

- **工作流工坊**
  - 导入任意 ComfyUI **Save (API Format)** JSON 工作流（未启动 GPU 时也可离线导入管理），自动生成带类型的参数表单。
  - 上传参考图片或音频素材，提交可取消的远端队列任务，并实时追踪节点执行进度。
  - 通过系统原生保存对话框预览并导出经 SHA-256 校验的图片、视频与音频输出；工作流（`/workflows/**`）与输出作品（`/comfyui-outputs/**`）会自动同步至 `ColabOne_Drive`。
  - 通过 `catalog/comfyui.json` 声明式管理远端模型与工作流资产包（纯数据清单，锁定版本修订号并执行 SHA-256 校验）。
- **原生画布**
  - 在你绑定的固定域名 Cloudflare Named Tunnel 后运行带令牌鉴权的 HTTP + WebSocket 反向代理（`127.0.0.1:8189` → `8188`）。
  - 使用每次会话随机生成的访问令牌换取限定作用域的 `HttpOnly` Cookie，直接在独立的 **ComfyUI 原生画布** 应用窗口或系统浏览器中打开。
- **严格显存互斥机制**
  - `llama-server`（`8081`）与 `ComfyUI`（`8188`）绝不同时占用 GPU 显存。切换引擎时会自动卸载当前引擎以释放显存，同时完整保留远端磁盘上的权重缓存。
  - 活跃的 ComfyUI 任务队列与原生画布请求都会自动暂停空闲自动释放倒计时。

## 🔒 存储、计费与安全

- **Google 云盘同步**：模型清单、ComfyUI 工作流与输出作品均使用受限的 `drive.file` 权限持久化至 `My Drive / ColabOne_Drive`（ColabOne 无法读取你个人云盘中的其他无关文件）。模型权重存放在 Colab 高速临时磁盘中，每次启动新实例均可重新高速拉取。卸载或切换模型不会删除当前活跃实例上已缓存的文件。
- **系统托盘与自动释放**：关闭窗口时，ColabOne 会最小化至系统托盘。退出应用会停止本地心跳，而远端虚拟机内的空闲自动释放守护仍会继续运行。网络断开不会停止 Colab 计费 —— 请务必在**远端设备**中确认实例已停止。
- **计费估算说明**：界面显示的剩余 CU 余额、`CU/小时` 消耗速率与预计可用时长均基于近期 Colab 接口查询结果计算，可能略滞后于官方账单。
- **凭据与隐私保护**：Google OAuth 凭据、API Key、Cloudflare Tunnel Token、可选的 Hugging Face Token（用于访问受限仓库）以及可选的仅分配阶段 GPU 分配代理配置均存储在操作系统凭据库中，并在日志中自动脱敏（内存保留最近 300 条记录，另写入权限为 `0600` 的本地活动日志）。ColabOne 与本机已有的 Colab CLI 配置完全独立。
- **能力边界与安全校验**：GGUF 导入均精确锁定 40 位提交 SHA 并对每个分片执行 SHA-256 校验，绝不执行仓库中的 Python 代码。早期内置桌面对话/生图/生视频页面已下线，统一由**对外 API** 与 **ComfyUI** 承接。v0.1 版本不包含 Agent 与本地 MCP 执行能力。开发构建版本未经商业代码签名或公证 —— 你随时可以审查源码或在本地自行构建。

## 🔧 从源码构建

### 📋 环境要求

- **Go** `1.27.3`
- **Node.js** `24 LTS`（或 `26`）
- **Python** `3.12` 与 **`uv`**（或标准 `venv` + `pip`）—— 仅用于构建与测试阶段（同步嵌入式远端服务包、运行远端 Python 单元测试与打包脚本）

### 🔨 安装依赖与构建

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

### 📦 校验与打包

> 在 Windows 上，请将 `.venv/bin/python` 替换为 `.venv\Scripts\python.exe`。

```sh
# 运行全部离线检查（嵌入式服务包同步校验、Go vet/test、远端 Python pytest、Vitest 与前端构建）
.venv/bin/python tools/install.py --root work/check --check

# 在 dist/ 目录构建纯 Go 单文件便携压缩包
.venv/bin/python tools/package.py --out dist
```

便携压缩包会输出至 `dist/`（解包暂存目录位于 `work/package-stage/Programs/ColabOne`），仅包含独立的纯 Go 主程序（`ColabOne.app` / `ColabOne.exe`），其内部已通过 `go:embed` 嵌入编译后的 Vue 前端资源与远端 Python 控制服务（`server/`、`common/`、`catalog/`），终端用户无需本机 Python 运行时或任何同级依赖目录。

所有单元测试与浏览器测试均 100% 基于 Mock 离线运行 —— 绝不会连接 Google 或 Hugging Face，也不会分配真实 GPU。真机 GPU 验收仅在严格算力预算下显式执行，详见 [docs/validation.md](docs/validation.md)。

## 📚 文档

- [架构概览](docs/architecture.md) — 纯 Go 进程内控制器、嵌入式远端服务、回环端口、云盘同步、硬件监控与安全边界
- [远端与对外 API 参考](docs/api.md) — 控制接口路由、OpenAI 网关契约与 ComfyUI 端点说明
- [验证记录](docs/validation.md) — 离线 CI 覆盖范围与真机 GPU 验收记录
- [上游来源与声明](third_party/SOURCES.md) — 锁定的第三方依赖清单与开源归属说明

## 🤝 参与贡献

欢迎随时提交代码贡献、问题反馈与功能建议！请参阅[贡献指南](CONTRIBUTING.md)，了解本地开发环境搭建、PR 规范与离线测试要求。

### 💖 贡献者

感谢每一位协助构建与改进 ColabOne 的贡献者：

<a href="https://github.com/luolongfei/ColabOne/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=luolongfei/ColabOne" alt="ColabOne 贡献者" />
</a>

## 🎉 致谢

特别感谢 **[Alma](https://alma.now/)** 与 **[Antigravity-Manager](https://github.com/lbjlaq/Antigravity-Manager)** —— ColabOne 的界面设计、视觉风格乃至应用 Logo 均深受这两个项目的启发与参考。

同时衷心感谢以下开源项目，正是因为有它们的开源分享与底层支撑，才有了今天的 ColabOne（完整清单与声明详见 [NOTICE](NOTICE) 与 [third_party/SOURCES.md](third_party/SOURCES.md)）：

- **[Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga)**（Apache-2.0） — 参考了 Google Colab GPU 分配、运行时就绪检测与心跳生命周期管理思路。
- **[EasyColab](https://github.com/wy51ai/easycolab)**（Apache-2.0） — 参考了桌面端到 Colab 的控制架构以及无头 `rclone` 挂载 Google 云盘的实现模式。
- **[colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service)**（MIT） — 参考了模型清单设计、固定版本 `llama.cpp` CUDA 部署及多线程分片下载策略。
- **[llama.cpp](https://github.com/ggml-org/llama.cpp)**（MIT）与 **[ComfyUI](https://github.com/Comfy-Org/ComfyUI)**（`v0.46.0`，GPL-3.0） — 分别作为远端 GGUF 大模型推理引擎与节点式图像、视频和多模态工作流引擎集成。
- **桌面端与前端 UI 技术栈** — **[MyGo](https://github.com/egoist/mygo)**（`v0.10.0`，MIT）、**[coder/websocket](https://github.com/coder/websocket)**（ISC）、**[Vue 3](https://github.com/vuejs/core)**、**[Vite](https://github.com/vitejs/vite)**、**[Ant Design Vue](https://github.com/vueComponent/ant-design-vue)**、**[Phosphor Icons](https://github.com/phosphor-icons/vue)**、**[Shiki](https://github.com/shikijs/shiki)**、**[flag-icons](https://github.com/lipis/flag-icons)**（MIT）与 **[Sarasa Gothic](https://github.com/be5invis/Sarasa-Gothic)**（SIL OFL 1.1）。
- **远端服务、隧道与云盘依赖** — **[FastAPI](https://github.com/fastapi/fastapi)**（MIT）、**[Google Colab CLI](https://github.com/googlecolab/google-colab-cli)**（Apache-2.0）、**[cloudflared](https://github.com/cloudflare/cloudflared)**（Apache-2.0）与 **[rclone](https://github.com/rclone/rclone)**（MIT）。

## 📜 开源许可

ColabOne 基于 **GNU General Public License v3.0**（[LICENSE](LICENSE)）开源，以匹配远端集成的 [ComfyUI](https://github.com/Comfy-Org/ComfyUI)（`v0.46.0`，GPL-3.0）。所有上游开源许可与版权声明均完整保留在 [NOTICE](NOTICE) 与 [third_party/SOURCES.md](third_party/SOURCES.md) 中。
