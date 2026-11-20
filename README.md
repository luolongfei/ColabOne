# ColabOne

一个管理 Google Colab 远端 GPU 的桌面客户端：启动并托管多台 GPU、下载并装载 GGUF 大模型、发布对外 OpenAI 兼容文本 API、运行远端 ComfyUI 工作流工坊与原生画布、持久化云盘作品、按需释放 GPU。

支持 macOS Apple Silicon 与 Windows x64。所有模型权重、模型装载、LLM 推理与 ComfyUI 工作流执行都在远端 GPU 运行。本机只运行桌面界面和控制进程。

## 首次使用

侧边栏按四阶段「**远端设备 → 对外 API → ComfyUI → 设置**」组织，运行日志保持右侧覆盖式抽屉：

1. 解压完整安装包，打开 `ColabOne.app` 或 `ColabOne.exe`。应用和旁边的 `runtime`、`site-packages`、`bridge` 等目录需要一起保留。
2. 在「设置」点击「登录 Google」，在浏览器选择账户并授权，完成后自动返回。若浏览器只显示回执，可展开「浏览器有一段回显？粘贴到这里」，整段粘贴或点击「粘贴并完成登录」。
3. 在「远端设备」选择 GPU，点击启动；支持在客户端内同时启动并切换托管多台 GPU 实例，也可独立查看同账号下其他客户端启动的实例规格。顶栏与设备视图会自动解析实例所在国家/地区（通过 GCP 可用区/端点与虚拟机元数据及地理回退），并以清晰内置矢量 SVG 国旗/区旗展示。常驻顶栏与「远端设备」首页概览卡片同步展示实时计费状态（计费中 / 未计费空闲）、每小时算力消耗速率（`CU/h`）、剩余 CU 余额以及人性化预计可用续航时间（`X 天 Y 小时`）；连接远端后，页面内嵌类 Windows 资源监视器风格的实时硬件图表，直观呈现 GPU 利用率与显存、系统内存（`/proc/meminfo`）、磁盘容量与实时读写 I/O 速率（`/proc/diskstats`）、CPU 利用率（`/proc/stat`）及 60 个采样点的滚动历史曲线。默认选择 T4，已取消单次启动 CU 预算上限（`max_cu`），统一采用可配置的空闲自动释放倒计时（默认 **30 分钟**，支持 `15 分钟`、`30 分钟`、`45 分钟`、`1 小时`、`2 小时`，可在首页下拉框或「设置」随时调整）：用户交互会自动重置倒计时，远端下载、显存装载、外部 API 推理以及 ComfyUI 队列执行与画布活跃请求期间自动暂停倒计时。启动时自动复用本机 Google 登录凭据无感挂载专属云盘目录 `My Drive / ColabOne_Drive`，一次登录长期有效，新建或切换实例无需浏览器二次确认。
4. 需要对外提供大模型文本接口时，进入「对外 API」，点击「选择模型 / 切换模型」打开右侧模型抽屉（双标签页：**常用模型** 与 **在线开源社区**）选定单个 GGUF 大模型：每张模型卡片均内联展示远端下载状态与显存装载状态徽章，支持内联调节上下文长度（`8K`–`64K`）和温度，点击「下载并装载」一步完成远端下载与 `llama.cpp`（loopback `8081`）显存装载（也可分步下载、快速切换或卸载模型）。「常用模型」默认自动收录 3 个常用社区模型仓库（`huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF`、`huihui-ai/Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF`、`JonathanColetti/Qwen3.8-27B-Uncensored`），支持随时点击「移除常用」手动调整；切换到「在线开源社区」可实时检索 GGUF 量化版本并点击「设为常用」或一键下载并装载，分片自动一起下载，也保留手动导入。下载并发支持 16 或 32 线程（默认 16），显示速率、已下载量、剩余时间和连接数。装好模型后直接在「对外 API」配置固定域名与 Cloudflare Tunnel 并开启服务。
5. 需要运行图像、视频或多模态节点工作流时，进入「ComfyUI」使用 **工作流工坊** 或 **原生画布**。
6. 使用完毕可针对单台实例点击「单独停止」，或点击「停止全部（N 个实例）」勾选需要停止的实例，保留其他客户端仍在运行的实例。本客户端托管实例在正常释放前会自动关闭对外隧道并同步云盘数据。停止后重新查询实例数和费率，连接中断不等于停止计费。

登录凭据与自动续期信息保存在本机的 macOS Keychain / Windows Credential Manager，重启客户端后自动恢复。Google 撤销或要求重新授权时才需再次登录。点击顶部日志按钮打开右侧抽屉，不挤占主区域。抽屉显示启动、授权、下载、装载、对外 API、ComfyUI 与停止进度，支持筛选、复制、清空与收起；日志仅保留本次运行最近 300 条，不记录提示词、授权链接或访问密钥。常驻顶栏与首页概览卡片实时区分计费中与空闲状态，并展示 `CU/h` 消耗速率、剩余 CU 余额及 `X 天 Y 小时` 预计续航；平台费率与余额可能存在延迟，以最近查询状态为准。

导入模型清单以及 ComfyUI 工作流（`/workflows/**`）与生成输出（`/comfyui-outputs/**`）自动持久化保存在 `My Drive / ColabOne_Drive`（权限限定于应用专属的 `drive.file` 域，不触碰个人云盘其他文件），也可在「ComfyUI」工作流工坊中经 SHA-256 校验后通过原生对话框导出到本地。模型权重存放在 Colab 临时高速磁盘，释放或平台回收后可重新下载。切换模型、卸载模型不会删除该实例已下载的其他模型。

关闭窗口后客户端留在托盘。退出客户端后，本机心跳停止，远端空闲自动释放倒计时保护仍继续运行；Colab 自身的资源限制仍然适用。显示的 CU 余额、`CU/h` 费率与预计续航时间基于最近查询估算，平台账单存在延迟，不能作为精确计费结算依据。

## 对外 API 服务

「对外 API」内嵌单模型侧边栏抽屉，聚焦 GGUF 大模型文本推理（远端 `llama.cpp` 运行于 loopback `8081`，对外认证网关运行于远端 loopback `7861`）。在「对外 API」点击「选择模型」打开抽屉装载单个 GGUF 模型后即可直接开启服务（开启后侧边栏状态灯会亮起在线指示）：

1. 固定域名接入：填写公网 HTTPS 固定域名，将 Cloudflare Tunnel 安装命令或 Token 整段粘贴到输入框。按照页内三步说明将域名映射到远端 `http://127.0.0.1:7861`，保存并开启服务（不支持临时 `trycloudflare.com` 域名；支持 SSE 流式 `stream=true` 与非流式 `stream=false`）。
2. 其他应用或 Codex 填入 Base URL、API Key 和当前模型 ID；网关开放带强制 Bearer 认证的 OpenAI 兼容接口 `/v1/models`、`/v1/chat/completions`、`/v1/responses` 与 Codex 上下文压缩接口 `/v1/responses/compact`，页面内提供 cURL / Python / Codex 接入示例。

密钥与 Tunnel Token 保存在本机系统凭据库。对外 API 默认关闭，不暴露 GPU 管理接口。外部推理请求期间自动暂停空闲释放倒计时。关闭 API 只停止公网访问；停止 GPU 会同时停止 API。详见 [接口与限制](docs/api.md#public-inference-api-llm-openai-gateway)。

## ComfyUI（工作流工坊与原生画布）

原桌面内置的「聊天」「画图」「视频」交互页及 `stable-diffusion.cpp`（`Z-Image Turbo`、`Wan2.2 5B`）固定单模型创作功能已移除，由侧边栏独立的 **ComfyUI** 入口统一承接视觉与多模态创作。ComfyUI（固定版本 `v0.46.0`）仅部署在远端 Colab GPU（loopback `8188`），采用双层 Mode 3 架构：

- **工作流工坊（Phase 1 通用底座）**：
  - 导入任意 ComfyUI API 格式 JSON 工作流，自动持久化至 `ColabOne_Drive/workflows/**`；
  - 自动解析工作流节点并生成可视化参数表单，支持按需上传输入媒体（如参考图）；
  - 提交可取消的远端队列执行并实时追踪节点进度；
  - 在作品画廊预览生成产物并自动持久化到 `ColabOne_Drive/comfyui-outputs/**`，支持经完整 SHA-256 校验后通过原生保存对话框导出到本地；
  - 通过可扩展的组件与资源包清单 `catalog/comfyui.json` 管理远端模型与工作流包（所有权重直接在远端下载校验，本机不下载权重）。
- **原生画布（认证反向代理 + 隧道）**：
  - 远端在 `127.0.0.1:8189` 启动带认证的 HTTP + WebSocket 反向代理，接入 `127.0.0.1:8188` 的 ComfyUI 引擎；
  - 使用随机访问令牌经认证入口换取 `HttpOnly` Cookie 会话，并通过固定域名 Cloudflared Named Tunnel 安全对外暴露；
  - 支持在独立桌面窗口（`ComfyUI 原生画布`）或系统浏览器中一键打开原生节点画布。
- **显存互斥与空闲保护**：
  - `llama-server`（`8081`）与 `ComfyUI`（`8188`）严格显存互斥，同一台 GPU 实例一次仅运行其中一个引擎；切换时自动卸载另一方并保留已下载的模型权重文件；
  - ComfyUI 队列任务执行与原生画布活跃请求期间自动暂停 30 分钟空闲自动释放倒计时。

## 开发

需要 Go 1.27.3、Node.js 24 LTS 或 26、Python 3.12，以及 `uv`（可用标准 venv/pip 替代）。

```sh
uv venv --python 3.12
uv pip install --python .venv/bin/python -r requirements-dev.txt
npm --prefix frontend ci
cd console
go tool mygo generate
cd ..
npm --prefix frontend run build
```

Windows 的 Python 路径为 `.venv\Scripts\python.exe`。以下命令用对应 Python 替换：

```sh
.venv/bin/python tools/install.py --root work/check --check
.venv/bin/python tools/package.py --out dist
```

开发安装包保存在 `dist/`，已解压安装目录在 `work/package-stage/Programs/ColabOne`。打包会下载并校验独立 CPython 与依赖，用户无需另装 Python、Go 或 Node。

所有单元测试均不连接 Google 或 Hugging Face，不分配 GPU。真实 GPU 验收需首次授权后显式执行并严格限定测试算力消耗，使用轻量模型，结束必须核实实例已释放。双平台 CI 构建并运行离线测试及打包运行时自检；未经过 Windows 人工交互验证时不可将其标记为已完成。

## 边界

- 支持同时创建并切换托管多台 ColabOne 实例（每台实例严格执行 `llama-server` 与 `ComfyUI` 显存互斥），并可独立展示账号下全部 Colab 实例规格，支持单实例释放或勾选释放，空闲自动释放保护针对本客户端托管实例。
- GGUF 导入固定到 commit SHA，校验全部文件；不执行模型仓库中的 Python 代码。需要授权的模型可配置 Hugging Face Token。
- 桌面端不再内置本地「聊天」「画图」「视频」交互页或 `stable-diffusion.cpp` 单模型创作配方；文本推理通过「对外 API」及其内置单模型选择抽屉（`/v1/models`、`/v1/chat/completions`）提供，图像/视频与节点化多模态生成统一通过「ComfyUI」工作流工坊与原生画布执行。超大模型需要足够的远端显存和磁盘，加载失败会报告远端引擎错误。
- Agent、本地 MCP、任意本地文件访问不在首版范围。
- 敏感配置使用 macOS Keychain / Windows Credential Manager；不读取或改写原 Yoga、colab-cli 的账号配置。
- 开发安装包未做商业代码签名或 macOS 公证。运行前可自行审阅代码并构建。

## 来源与许可

主项目使用 **GNU GPL-3.0** 许可证（见 [LICENSE](LICENSE)），以匹配远端集成的 [ComfyUI](https://github.com/Comfy-Org/ComfyUI)（GPL-3.0，`v0.46.0`）；同时完整保留 [Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga)、EasyColab、Google Colab CLI 与 cloudflared 等上游 Apache-2.0 声明，以及 [colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service) 与 rclone 等上游 MIT 许可声明。详情见 [NOTICE](NOTICE)、[来源记录](third_party/SOURCES.md)、[架构](docs/architecture.md) 和 [API](docs/api.md)。
