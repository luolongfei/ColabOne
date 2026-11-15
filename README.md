# ColabOne

一个管理 Google Colab 远端 GPU 的桌面客户端：启动 GPU、下载 GGUF、加载模型、聊天、保存会话、释放 GPU。

支持 macOS Apple Silicon 与 Windows x64。所有模型权重、模型加载与推理都在远端执行。本机只运行桌面界面和控制进程。

## 首次使用

1. 解压完整安装包，打开 `ColabOne.app` 或 `ColabOne.exe`。应用和旁边的 `runtime`、`site-packages`、`bridge` 等目录需要一起保留。
2. 在「设置」点击「登录 Google」，在浏览器选择账户并授权，完成后自动返回。若浏览器只显示回执，可展开「浏览器有一段回显？粘贴到这里」，整段粘贴或点击「粘贴并完成登录」。
3. 在「远端设备」选择 GPU，点击启动。默认 T4，单次预算 5 CU，闲置满 3 分钟后开始 60 秒倒计时，继续操作会重置；下载、加载和生成期间暂停闲置释放。只有主动启动才会分配 GPU。
4. 默认会接着连接 Google 云盘：浏览器确认 Colab 的云盘授权后自动继续。无需安装 Tailscale、创建 Google Cloud 项目、申请 API Key 或下载 OAuth JSON。客户端先尝试复用已有授权，仅在需要时打开确认页；确认过程中会持续等待，不会因临时 HTTP 400 立即报错。新建实例时 Google 仍可能要求重新确认挂载权限，这与重新登录账户不同。也可点击「稍后连接」，之后在设置中继续。
5. 在「模型库」点击下载到远端，完成后点击加载，再进入聊天。建议先用 Qwen2.5 0.5B 验证链路。点击「在线搜索」实时检索 Hugging Face，选择量化版本后直接添加并下载，分片自动一起下载；也保留手动导入。下载可选 8 或 32 线程，显示速率、已下载量、剩余时间和连接数。
6. 使用完毕点击红色「停止全部（N 个实例）」。查看当前 Google 账号的实例清单，再确认停止；包括在其他 Colab 页面启动的实例。ColabOne 当前实例会先停止任务、同步云盘并等待上传完成，其他实例请先保存工作。停止后重新查询实例数和费率，连接中断不等于停止计费。

登录凭据与自动续期信息保存在本机的 macOS Keychain / Windows Credential Manager，重启客户端后自动恢复。Google 撤销或要求重新授权时才需再次登录。点击顶部日志按钮打开右侧抽屉，不挤占主区域。抽屉显示启动、授权、下载、加载、对话和停止进度，支持筛选、复制、清空与收起；日志仅保留本次运行最近 300 条，不记录聊天内容或授权链接。正在计费时，费率与提示图标为红色；费率可能有延迟，以最近查询状态为准。

easycolab 也只是封装 `drive.mount()`，没有跨实例永久免授权机制；其旧 `tkmount()` 授权码只能使用一次。ColabOne 保留 Google 登录自动续期，但不会把每台 GPU 的临时挂载授权伪装成永久凭据。

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

- 首版使用一个由当前 ColabOne 配置创建的实例执行模型与聊天任务，一次加载一个模型。账号清单可显示全部 Colab 实例；只有主动确认「停止全部」才会释放清单中的其他实例，预算与空闲保护仅针对当前实例。
- GGUF 导入固定到 commit SHA，校验全部文件；不执行模型仓库中的 Python 代码。需要授权的模型可配置 Hugging Face Token。
- 上游预置模型的权重与来源保留；首版只启用文本推理，不加载视觉投影、MTP 草稿或 LoRA。超大模型需要足够的远端显存和磁盘，加载失败会报告远端引擎错误。
- Agent、MCP、本地文件访问、图像输入、跨设备同时写入同一会话不在首版范围。
- 敏感配置使用 macOS Keychain / Windows Credential Manager；不读取或改写原 Yoga、colab-cli 的账号配置。
- 开发安装包未做商业代码签名或 macOS 公证。运行前可自行审阅代码并构建。

## 来源与许可

以 [Colab-yoga](https://github.com/incredible404-rgb/Colab-yoga) 的 Apache-2.0 代码为基础，合入 [colab-gpu-llm-service](https://github.com/incredible404-rgb/colab-gpu-llm-service) 的 MIT 模型清单及运行机制。详情见 [来源记录](third_party/SOURCES.md)、[架构](docs/architecture.md) 和 [API](docs/api.md)。主项目使用 Apache-2.0，MIT 部分保留原许可证。
