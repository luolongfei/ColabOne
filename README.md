# ColabOne

一个管理 Google Colab 远端 GPU 的桌面客户端：启动 GPU、下载 GGUF、加载模型、聊天、保存会话、释放 GPU。

支持 macOS Apple Silicon 与 Windows x64。所有模型权重、模型加载与推理都在远端执行。本机只运行桌面界面和控制进程。

## 首次使用

1. 解压完整安装包，打开 `ColabOne.app` 或 `ColabOne.exe`。应用和旁边的 `runtime`、`site-packages`、`bridge` 等目录需要一起保留。
2. 在「设置」点击「登录 Google」，在浏览器选择账户并授权，完成后自动返回。若浏览器只显示回执，可展开「浏览器有一段回显？粘贴到这里」，整段粘贴或点击「粘贴并完成登录」。
3. 在「远端设备」选择 GPU，点击启动。默认 T4，单次预算 5 CU，空闲 30 分钟释放。只有主动启动才会分配 GPU。
4. 默认会接着连接 Google 云盘：浏览器确认 Colab 的云盘授权后自动继续。无需安装 Tailscale、创建 Google Cloud 项目、申请 API Key 或下载 OAuth JSON。新建实例时 Google 可能要求重新确认授权。也可点击「稍后连接」，之后在设置中继续。
5. 在「模型库」点击下载到远端，完成后点击加载，再进入聊天。建议先用 Qwen2.5 0.5B 验证链路。导入其他 GGUF 时填写 Hugging Face 仓库和文件路径。
6. 使用完毕点击释放 GPU。客户端先停止任务、写入云盘并等待上传完成，再确认该实例已不在 Colab 分配列表中。连接中断不等于停止计费。

云盘通过 Colab 自带的 Drive 挂载连接；会话和模型配置在「我的云端硬盘 / ColabOne」中。只有运行实例并连接云盘后才能从云端恢复历史；同一 Google 账户在 Mac 和 Windows 上可恢复同一份文件。未连接时会话暂存在远端，可在聊天页导出 Markdown。权重存放在 Colab 临时磁盘，释放或平台回收后需要重新下载。切换模型、卸载模型不会删除已下载的其他模型。

关闭窗口后客户端留在托盘。退出客户端后，本机心跳停止，远端空闲与预算释放程序仍运行；Colab 自身的资源限制仍然适用。预算以账户费率、余额变化和预留时间提前保护，账单存在延迟，不能作为精确计费结算依据。

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

所有单元测试均不连接 Google 或 Hugging Face，不分配 GPU。真实 GPU 验收需要首次授权完成，默认预算上限 5 CU，使用轻量模型，结束必须核实实例已释放。双平台 CI 构建并运行离线测试及打包运行时自检；未经过 Windows 人工交互验证时不可将其标记为已完成。

## 边界

- 首版只管理一个由当前 ColabOne 配置创建的实例，一次加载一个模型，一次执行一个下载、加载或生成任务。
- GGUF 导入固定到 commit SHA，校验全部文件；不执行模型仓库中的 Python 代码。需要授权的模型可配置 Hugging Face Token。
- 上游预置模型的权重与来源保留；首版只启用文本推理，不加载视觉投影、MTP 草稿或 LoRA。超大模型需要足够的远端显存和磁盘，加载失败会报告远端引擎错误。
- Agent、MCP、本地文件访问、图像输入、跨设备同时写入同一会话不在首版范围。
- 敏感配置使用 macOS Keychain / Windows Credential Manager；不读取或改写原 Yoga、colab-cli 的账号配置。
- 开发安装包未做商业代码签名或 macOS 公证。运行前可自行审阅代码并构建。

## 来源与许可

以 [Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga) 的 Apache-2.0 代码为基础，合入 [colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service) 的 MIT 模型清单及运行机制。详情见 [来源记录](third_party/SOURCES.md)、[架构](docs/architecture.md) 和 [API](docs/api.md)。主项目使用 Apache-2.0，MIT 部分保留原许可证。
