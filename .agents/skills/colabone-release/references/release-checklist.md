# ColabOne 发布检查清单

## 文件映射

以下路径均相对于 ColabOne 仓库根目录：

- `console/mygo.json`：桌面端应用清单，`version` 字段同时被 `console/splash.go` 嵌入到 Go 二进制、`frontend/vite.config.ts` 注入为 `__COLABONE_APP_VERSION__`、以及 `tools/package.py` 用作压缩包文件名版本
- `frontend/package.json`：前端包清单，`version` 字段与 `console/mygo.json` 保持同步
- `frontend/package-lock.json`：npm 锁文件，顶层 `version` 与 `packages[""].version` 均与 `console/mygo.json` 保持同步
- `tools/release_version.py`：CI 与本地共用的版本清单校验/更新工具（`--check`、`--tag`、`--set`）
- `tools/install.py`：离线检查入口（`python3 tools/install.py --root <tmp> --check`），依次执行 Python 字节码编译、只读嵌入式远端服务包一致性校验（`check_service_bundle`，不做写入同步；更新远端源码后需手动运行 `python3 tools/sync_service_bundle.py`）、`mygo generate` 绑定一致性校验、前端构建、`go vet`、`go test`、`pytest tests`、`vitest` 与纯 Go `go run . --self-test`
- `tools/package.py`：便携包打包与敏感信息审计脚本，在 CI 中分别产出 `ColabOne-<version>-windows-x64.zip` 与 `ColabOne-<version>-macos-arm64.zip`
- `.github/workflows/ci.yml`：仅响应 `workflow_call` 与 `workflow_dispatch`，在 `windows-latest` 和 `macos-latest` 上运行离线检查、Playwright UI 回归测试并上传压缩包 artifact
- `.github/workflows/release.yml`：响应 `v*` 标签推送与手动 `workflow_dispatch`；依次校验标签与提交、校验三处源码版本、调用 `ci.yml` 构建双平台便携包、生成 `SHA256SUMS.txt` 并创建/更新 GitHub Release
- `.agents/skills/colabone-release/scripts/prepare_release.py`：发布准备脚本，默认将最后一位（patch）`+1`，原子更新三处版本清单并输出中英双语（上方中文区块、下方英文区块）附注标签发布说明
- `.agents/skills/colabone-release/scripts/github_actions.py`：GitHub Actions 查询、日志下载、安全重跑失败任务与 GitHub Release 产物验收脚本
- `tests/test_release_workflow.py` 与 `tests/test_colabone_release_skill.py`：覆盖版本脚本、CI/Release 工作流契约、发布准备脚本及 Actions 客户端的离线单元测试

## 版本行为

- **默认策略**：未传 `--version` 时，总是将版本号最后一位（patch）`+1`：
  - 已有 `v*` 标签时：取最高 SemVer 标签，校验三处源码清单均等于该标签版本，然后递增 patch（如 `v0.1.1` → `0.1.2`）
  - 尚无 `v*` 标签时：校验三处源码清单一致，直接对当前源码版本递增 patch（如 `0.1.0` → `0.1.1`）
  - 最新版本为预发布版（如 `0.2.0-rc.1`）时：拒绝自动推断，要求显式提供 `--version`
- **显式指定版本**：支持 `MAJOR.MINOR.PATCH` 或 `MAJOR.MINOR.PATCH-prerelease`（可选 `v` 前缀）；拒绝前导零、已存在标签、旧于/等于最新已有标签的版本以及降级当前源码版本
- `console/splash.go`、`frontend/src/colabOneVersion.ts` 与 `tools/release_version.py` 中的 `0.1.0` 常量仅作兜底默认值；`server/app.py` 的 `/health` 接口版本契约不属于发版清单，不要改动

## 准备失败后的恢复

`prepare_release.py` 会先在同目录写出临时文件并 `fsync`，替换前备份原文件；任意一步失败时按逆序自动回滚已替换文件。若极端情况下回滚本身失败，报错信息会列出受影响文件及保留的备份副本路径：先解除文件占用或权限问题，用备份还原并运行 `python3 tools/release_version.py --check` 确认一致后，再重新执行发布准备。

## 离线验证命令

WSL2/Linux 环境下请始终设置 `PATH`、`LD_LIBRARY_PATH` 并重定向 `< /dev/null`：

```bash
export PATH="$HOME/.local/bin:$PATH"
export LD_LIBRARY_PATH="$HOME/.local/sysroot/usr/lib/x86_64-linux-gnu:${LD_LIBRARY_PATH:-}"

python3 tools/release_version.py --tag "v$VERSION" < /dev/null
python3 tools/install.py --root "$(mktemp -d)" --check < /dev/null
git diff -- console/mygo.json frontend/package.json frontend/package-lock.json
```

## 附注标签与推送

必须使用附注标签，因为 `.github/workflows/release.yml` 读取 `%(contents:subject)` 与 `%(contents:body)` 作为 GitHub Release 说明。将脚本实际输出的发布说明路径赋给 `RELEASE_NOTES_FILE`：

```bash
git add console/mygo.json frontend/package.json frontend/package-lock.json
git commit -m "chore: release $VERSION"
git tag -a "v$VERSION" -F "$RELEASE_NOTES_FILE" --cleanup=verbatim

# 核对标签指向与双语渲染正文（确认上方中文区块、中间 ---、下方英文区块）
git rev-parse "v${VERSION}^{commit}"
git for-each-ref --format='--- SUBJECT ---%0a%(contents:subject)%0a--- BODY ---%0a%(contents:body)' "refs/tags/v${VERSION}"

# 先推分支，再推标签
git push origin main
git push origin "v$VERSION"
```

## 推送后的验收

- 按 [references/github-actions.md](github-actions.md) 持续跟踪 `.github/workflows/release.yml` 对应标签和提交 SHA 的运行结果
- 全部四个阶段均需成功：`Validate release metadata`、`check (windows-latest)`、`check (macos-latest)`、`Publish GitHub Release`
- 最终核实 GitHub Release 非草稿、预发布标记与标签一致、中英双语发布说明（上方中文区块、下方英文区块）完整写入，且包含完整且非空的 7 个必选附件：
  - `ColabOne-<version>-windows-x64.zip`
  - `ColabOne-<version>-macos-arm64.zip`
  - `colabone-<version>-windows-amd64.tar.gz`
  - `colabone-<version>-darwin-arm64.tar.gz`
  - `update-windows-amd64.json`
  - `update-darwin-arm64.json`
  - `SHA256SUMS.txt`（以及如有前序签名版本时的 `.delta` 增量补丁）
- 推送标签本身不代表发布完成；只有工作流全部成功且 Release 附件验收通过后，才报告发布完成
