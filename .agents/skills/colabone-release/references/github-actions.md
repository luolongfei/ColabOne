# GitHub Actions 跟进、恢复与 Release 验收

## 范围与完成标准

本说明用于发布前只读权限探测、推送标签后的工作流跟进、失败任务诊断恢复以及最终 GitHub Release 产物验收。继续已有发布时，不运行 `prepare_release.py`，不递增版本，不重新提交，也不强制移动已推送的标签。

- `.github/workflows/release.yml` 负责整条发布链路：
  1. `Validate release metadata`：核验 SemVer 标签、提交 SHA 和三处源码版本清单
  2. `Build and check packages`（复用 `.github/workflows/ci.yml`）：在 `windows-latest` 与 `macos-latest` 上执行离线检查、Playwright UI 回归测试和原生安装包打包审计
  3. `Publish GitHub Release`：确认标签未漂移、下载双平台安装包与自动更新产物、生成 `SHA256SUMS.txt` 并发布 GitHub Release
- 发布完成必须同时满足：
  1. 匹配当前 `release.yml` + `push` + `vX.Y.Z` + 精确 `git_sha` 的运行状态为 `completed` 且结论为 `success`
  2. GitHub Release 已创建（非 draft），`prerelease` 标记与版本号一致，中英双语发布说明（上方中文区块、下方英文区块）完整写入，且全部 7 个必选附件均为 `uploaded` 且字节数大于 0：`ColabOne-<version>-windows-x64-setup.exe`、`ColabOne-<version>-macos-arm64.dmg`、`colabone-<version>-windows-amd64.tar.gz`、`colabone-<version>-darwin-arm64.tar.gz`、`update-windows-amd64.json`、`update-darwin-arm64.json`、`SHA256SUMS.txt`（如存在前序签名版本还会附带 `.delta` 增量补丁）

## 工具、凭据和权限

优先使用已登录的 `gh`；不要求为本任务临时安装或交互登录 `gh`。当 `gh` 不可用或不适用时，使用本 skill 提供的 Python 标准库脚本 `.agents/skills/colabone-release/scripts/github_actions.py`。脚本支持 `github.com` 的 HTTPS 与 SSH remote，默认根目录由脚本位置自动定位，也可用 `--repo-root` 显式指定。

脚本的输出约定：
- 操作成功退出 `0`，运行时错误退出 `1`，参数或 `--help` 遵循 argparse 约定（`0` / `2`）
- 正常输出与运行时错误均为标准 JSON（非 ASCII 字符使用标准 JSON 转义，避免非 UTF-8 终端或管道报错）；下载的日志文件保持原始字节

凭据读取优先级固定为：
1. `GH_TOKEN`
2. `GITHUB_TOKEN`
3. `gh auth token --hostname github.com`
4. 仅当 remote 为 `https://` 时，调用非交互式 `git credential fill`（`GIT_TERMINAL_PROMPT=0`、`GCM_INTERACTIVE=never`）

SSH 密钥不是 GitHub REST API Token；使用 SSH remote 时需提供前三种 Token 来源之一。脚本不修改 git remote、登录态或凭据存储，也不将 Token 写入输出。

| 操作 | 细粒度 Token 仓库权限 |
| --- | --- |
| 查询工作流运行、任务、下载日志、预检状态 | Actions: Read |
| 查询 GitHub Release 及附件 | Contents: Read |
| 触发 `ci.yml` 双平台预检、重跑失败任务 | Actions: Read and write |

发布前应先执行一次只读查询核实读权限；只读查询成功不代表具备写权限，不要用重跑来试探写权限。遇到 HTTP 401/403/404 时保留完整状态码、响应头和响应体并一次向用户说明需检查的权限与网页操作链接（用户也可直接在 GitHub 页面触发 `ci.yml` 或点击 **Re-run failed jobs**）。

> `gh run watch` 不兼容部分细粒度 PAT；轮询时优先采用周期性 REST 查询，不要求用户放宽 Token 权限。

## 发版前双平台预检（`ci.yml`）

在修改版本号或创建任何 `v*` 标签之前，先将功能/修复提交推送到 `origin/main`，并触发 `ci.yml` 验证 `windows-latest` 与 `macos-latest`（完整覆盖离线测试、Playwright UI 回归及原生打包自检）：

```bash
PREFLIGHT_SHA=$(git rev-parse HEAD)
SCRIPT=.agents/skills/colabone-release/scripts/github_actions.py

# 1. 触发 ci.yml 预检（若同 SHA 已有全绿或运行中记录则幂等跳过，加 --force 可强制重跑）
python3 "$SCRIPT" dispatch --workflow ci.yml --ref main --sha "$PREFLIGHT_SHA" < /dev/null

# 2. 轮询预检运行列表与双平台任务状态
python3 "$SCRIPT" list --workflow ci.yml --branch main --event workflow_dispatch --sha "$PREFLIGHT_SHA" < /dev/null
python3 "$SCRIPT" preflight-status --branch main --sha "$PREFLIGHT_SHA" < /dev/null

# 3. 确认 windows-latest 与 macos-latest 均已完成且成功（未就绪时退出码为 1）
python3 "$SCRIPT" preflight-status --branch main --sha "$PREFLIGHT_SHA" --require-green < /dev/null

# 如预检遇到外部网络抖动失败，可在核实日志后对预检运行执行一次失败任务重跑：
python3 "$SCRIPT" rerun-failed --run-id "$RUN_ID" \
  --workflow ci.yml --branch main --event workflow_dispatch \
  --sha "$PREFLIGHT_SHA" --attempt "$ATTEMPT" < /dev/null
```

- `dispatch` 会将 `inputs.ref` 精确锁定为 40 位 `$PREFLIGHT_SHA`，防止后续推入 `main` 的新提交干扰本次预检
- 若传了 `--created-after "<dispatched_at>"`，`list` 与 `preflight-status` 仅匹配本次触发之后创建的运行
- 只有在 `preflight-status --require-green` 返回 `ready: true`（`windows-latest` 与 `macos-latest` 均为 `success`）之后，才运行 `prepare_release.py` 并打正式 `v*` 标签

## 定位运行、读取日志与验收 Release

从附注标签解析完整 40 位提交 SHA，并查询 `release.yml` 运行：

```bash
RELEASE_TAG=v0.1.1
RELEASE_SHA=$(git rev-parse "${RELEASE_TAG}^{commit}")
SCRIPT=.agents/skills/colabone-release/scripts/github_actions.py

# 1. 定位本次标签推送触发的 release.yml 运行
python3 "$SCRIPT" list --workflow release.yml --tag "$RELEASE_TAG" --sha "$RELEASE_SHA" < /dev/null

# 2. 查看运行与各任务状态（按当前尝试或指定历史尝试）
python3 "$SCRIPT" show --run-id "$RUN_ID" < /dev/null
python3 "$SCRIPT" show --run-id "$RUN_ID" --attempt "$ATTEMPT" < /dev/null

# 3. 下载失败任务原始日志或整次尝试日志 ZIP（输出路径必须尚不存在）
python3 "$SCRIPT" logs --job-id "$JOB_ID" --output "$JOB_LOG_PATH" < /dev/null
python3 "$SCRIPT" logs --run-id "$RUN_ID" --attempt "$ATTEMPT" --output "$RUN_LOG_ARCHIVE_PATH" < /dev/null

# 4. 验收 GitHub Release 与 7 个预期附件
python3 "$SCRIPT" release --tag "$RELEASE_TAG" < /dev/null
```

轮询与排查注意事项：
- 标签刚推送后短时间内 `list` 可能为空，等待 10–30 秒后复查，切勿把空列表当作成功
- 若存在多个匹配运行，结合运行 ID、创建时间与发布上下文核对，不盲取第一项；手动 `workflow_dispatch` 触发的发布不使用 `list` 的 `push` 过滤，应通过 `show --run-id "$RUN_ID"` 或 `gh run list --workflow release.yml --event workflow_dispatch` 核对
- 默认每 30 秒查询一次，无明显进展时放宽到 60 秒；任务运行中时日志可能尚不可下载，继续查询状态即可
- `logs` 对 302 签名日志地址采用独立的无认证 HTTPS 请求（不转发 GitHub `Authorization`），校验 `Content-Length`，下载失败时自动删除半文件且绝不覆盖已有文件

若本机已安装并登录 `gh`，等价常用命令如下（同样需核对 workflow、tag、SHA 与 attempt）：

```bash
gh run list --workflow release.yml --commit "$RELEASE_SHA" --branch "$RELEASE_TAG" --event push \
  --json databaseId,headBranch,headSha,status,conclusion,url
gh run view "$RUN_ID" --json databaseId,attempt,headBranch,headSha,status,conclusion,jobs,url
gh run view "$RUN_ID" --attempt "$ATTEMPT" --log-failed
gh run rerun "$RUN_ID" --failed
gh release view "$RELEASE_TAG" --json tagName,name,body,isDraft,isPrerelease,url,assets
```

## 失败恢复与停止条件

1. 先通过 `show` 和 `logs` 读取失败任务日志，确认具体失败阶段（元数据校验、Windows/macOS 测试或打包、还是 Release 发布）。
2. 区分问题类型：
   - **源码/测试/工作流脚本缺陷**：不能靠重跑旧标签获取新代码；必须先在代码仓库修复问题，再按新版本流程发布下一版本（版本号最后一位 `+1`），严禁删除或强制移动已推送的发布标签
   - **外部临时故障**（如 runner 瞬断、下载依赖超时、临时网络抖动等）：确认原因后，允许对本次已授权的标签与 SHA 自动执行一次 `rerun-failed`
3. 同因再次失败时立即停止自动重跑，保留运行链接与日志证据并向用户报告阻塞。运行中、排队中、已取消或已成功的运行均不自动重跑。
4. 仅调用失败任务重跑端点，不自动重跑全部任务，不自动取消任务：

```bash
python3 "$SCRIPT" rerun-failed --run-id "$RUN_ID" \
  --workflow release.yml --tag "$RELEASE_TAG" \
  --sha "$RELEASE_SHA" --attempt "$ATTEMPT" < /dev/null
```

重跑安全保证：
- 脚本在 `POST` 前重新读取运行并严格核验 workflow、`event=push`、tag、SHA、`run_attempt` 以及 `status=completed` + `conclusion in {failure, timed_out}`
- 仅当服务端返回 HTTP `201` 时确认重跑已被接受；之后仍需持续查询新的 `run_attempt` 和所有任务直到完成
- 若 `POST` 出现网络异常、HTTP `408`/`5xx` 或非 `201` 状态，脚本只读复查一次运行状态：若已观察到更高的 `run_attempt` 则返回成功证据；否则返回 `rerun_outcome_unknown` 且绝不自动二次 `POST`（此时继续只读查询或查看网页确认）
