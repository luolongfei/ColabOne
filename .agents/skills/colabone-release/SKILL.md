---
name: colabone-release
description: >-
  Prepare, verify, tag, publish, or resume ColabOne releases using remote
  GitHub Actions (Windows + macOS CI inside release.yml) without redundant
  local test/Playwright checks, including automatic patch (+1) version bumping,
  manifest synchronization, bilingual (Chinese + English) annotated release
  notes, GitHub Actions tracking, same-version failure recovery, and GitHub
  Release artifact verification.
---

# ColabOne Release

## 项目级使用与路径

本 skill 随 ColabOne 仓库维护，入口是 `.agents/skills/colabone-release/SKILL.md`。拉取包含此目录的仓库后，Antigravity 与 Codex 会自动发现本 skill，无需复制到用户目录。

- `scripts/`、`references/` 和 `agents/` 相对于本 skill 目录；其余仓库路径与 shell 命令相对于 ColabOne 仓库根目录
- `scripts/prepare_release.py` 与 `scripts/github_actions.py` 根据自身位置定位仓库根目录，不依赖用户名、克隆路径或当前工作目录；可用 `--repo-root` 显式指定检出目录
- 发布说明默认写入操作系统临时目录（`colabone-vX.Y.Z-release-notes.md`），可用 `--notes-output` 指定路径；打标签时始终使用脚本实际输出的路径，不假设临时目录位置
- 必须先完成受版本控制的版本清单更新与提交，再创建**附注标签**；`.github/workflows/release.yml` 会读取附注标签的标题和正文作为 GitHub Release 说明
- 在 WSL2/Linux 非交互式 shell 中执行命令与脚本前，先将 `$HOME/.local/bin` 放到 `PATH` 最前、将 `$HOME/.local/sysroot/usr/lib/x86_64-linux-gnu` 放到 `LD_LIBRARY_PATH` 最前，并重定向标准输入（`< /dev/null`）；不要在 Linux shell 中调用 Windows 的 `/mnt/c/.../node.exe` 或 `npx.cmd`

## 远端发布检查与同版本号直至成功原则

- **远端发布检查优先（不在本地执行重复测试与预检）**：
  - 发布阶段**不在本地执行** `tools/install.py --check` 或本地 Playwright UI 回归测试（`--with-e2e`），也**不要求**在打标签前单独跑一轮冗余的 `ci.yml` 预检。
  - 本地仅执行秒级元数据与版本清单校验（同步远端标签、检查分支与工作区、`python3 tools/release_version.py --check` / `--tag "v$VERSION"`、核对三处版本文件 diff 与附注标签正文），随后直接提交、打附注标签并推送，由远端 `.github/workflows/release.yml` 统一在 `windows-latest` 与 `macos-latest` 上一次性完成全量离线测试、Playwright UI 回归、原生安装包构建、自检与 Release 发布。
  - 若用户明确要求单独做一次不打标签的双平台远程诊断，仍可按需使用 `scripts/github_actions.py` 的 `dispatch --workflow ci.yml` 与 `preflight-status --require-green`（常规发版流程默认跳过，直接进入 `release.yml` 远端发布检查）。
- **同版本号直至发布成功原则（零空版本 / 零不可用版本）**：
  - 远端 `release.yml` 在构建、测试或验收阶段遇到任何源码、测试或打包缺陷时，**绝不因为线上检测失败就将版本号 `+1` 另发新版本**。
  - 正确做法：读取远端失败日志定位根因，直接在 `main` 上修复问题（保持三处版本清单仍锁定为当前目标版本 `X.Y.Z`，不递增至下一版本号），删除该版本残留的失败、草稿或不完整 GitHub Release（如已创建）以及远端和本地的旧 `vX.Y.Z` 标签（`git push origin :refs/tags/vX.Y.Z`、`git tag -d vX.Y.Z`），在修复后的提交上重新创建附注标签 `vX.Y.Z` 并推送触发远端重新发布，持续迭代直到同一个版本 `vX.Y.Z` 完整通过并可用。
- **继续已有发布（非源码故障）**：仅因外部网络/runner 抖动中断时，复用原标签与提交 SHA，不递增版本，不重新运行准备脚本、不重复提交。
- 推送标签前、推送标签后以及查询或恢复已有发布时，先读取 [references/github-actions.md](references/github-actions.md)。
- 发布前使用只读请求检查 GitHub Actions 访问能力；Git 可推送不代表 Actions API 可读写，不要通过重跑探测写权限。
- 优先使用已认证且可用的 `gh`；无 `gh` 或命令不适用时，使用 `scripts/github_actions.py`（仅依赖 Python 标准库，复用现有凭据，无需临时编写 API 脚本）。

## 版本选择规则

- **默认行为（未指定版本）**：每次发版默认将版本号最后一位（patch）`+1`：
  - 若仓库已有 `v*` 标签，取最新 SemVer 标签，校验源码版本清单与该标签完全一致后递增 patch（例如 `v0.1.1` 递增至 `0.1.2`）
  - 若仓库尚无 `v*` 标签，校验源码版本清单一致后直接从当前源码版本递增 patch（例如 `0.1.0` 递增至 `0.1.1`）
  - 若最新标签或当前版本为预发布版（如 `0.2.0-rc.1`），不自动猜测下一预发布号或正式号，停止并要求显式指定 `--version`
- **显式指定版本**：用户明确给出版本时（如 `0.2.0`、`v0.2.0`、`0.2.0-rc.1`），规范化可选 `v` 前缀，校验合法 SemVer、目标标签不存在、严格高于最新已有标签且不降级当前源码版本
- 在修改任何文件前只确定一次目标版本；版本清单、提交说明、发布说明、附注标签与推送全程使用同一版本
- 除非用户明确要求，否则绝不自行修改 major 或 minor

## 发布流程

1. **检查仓库与标签状态**
   - 同步远端标签：`git fetch --tags --prune origin`
   - 检查分支、工作区与最近标签：`git branch --show-current`、`git status --short`、`git tag --list 'v*' --sort=-v:refname | head -n 10`
   - 校验三处版本清单当前一致：`python3 tools/release_version.py --check`
   - 检查工作区全部未提交改动；不要将无关改动混入发布提交，**禁止**执行 `git add .` 或 `git add -A`
   - 若存在待发布的功能/修复改动，先按常规提交单独提交并推送到 `main`（此时不改版本号）
   - 查看自上次发布以来的提交以归纳用户可见变更：`git log <latest_tag>..HEAD --oneline`（首次发版时查看最近提交记录）
   - 按 [references/github-actions.md](references/github-actions.md) 做一次只读 Actions 权限探测
2. **准备发布版本与中英双语发布说明**
   - 可先加 `--dry-run` 预览解析出的目标版本、更新文件列表与中英双语发布说明，确认无误后再正式写入
   - 运行 `scripts/prepare_release.py`，**必须同时传入成对的中英文发布说明条目**：
     - 中文区块（上方）：`--added-zh` / `--changed-zh` / `--fixed-zh`（兼容 `--added` / `--changed` / `--fixed` 别名）
     - 英文区块（下方）：`--added-en` / `--changed-en` / `--fixed-en`
     - 未指定版本时省略 `--version`（默认最后一位 `+1`）
   - 脚本会强校验双语完整性：任一有内容的分类都必须同时提供数量一致的中文与英文条目，否则拒绝写入；确认通过后原子更新 `console/mygo.json`、`frontend/package.json`、`frontend/package-lock.json`（含 `packages[""].version`），并生成附注标签用的中英双语发布说明文件（写入失败自动回滚）
   - 发布说明面向用户，简洁描述可见功能、体验改进或问题修复，不直接照搬内部实现细节或重构流水账；英文区块应使用自然地道的英文准确表达对应变更（避免生硬直译），便于国际用户直接阅读
3. **快速核对版本清单与 Diff（不做本地测试）**
   - 使用与 CI 完全一致的校验器核对目标标签：`python3 tools/release_version.py --tag "v$VERSION"`
   - 核对 diff，确认三处版本文件仅包含预期版本变更（无任何业务代码变动）：
     `git diff -- console/mygo.json frontend/package.json frontend/package-lock.json`
   - **不在本地运行** `tools/install.py --check` 或本地 Playwright，直接交给远端 `release.yml` 在 Windows + macOS 上执行完整检查与打包
   - 详细检查项见 [references/release-checklist.md](references/release-checklist.md)
4. **提交、创建附注标签并推送触发远端发布检查**
   - 仅暂存受版本清单管理的文件：
     `git add console/mygo.json frontend/package.json frontend/package-lock.json`
   - 提交说明格式固定为：`chore: release X.Y.Z`
   - 将脚本输出的发布说明实际路径赋给 `RELEASE_NOTES_FILE`，创建附注标签并保留 Markdown 格式：
     `git tag -a "vX.Y.Z" -F "$RELEASE_NOTES_FILE" --cleanup=verbatim`
   - 核对标签指向提交及标签内容，再先推分支、后推标签：
     `git push origin main`
     `git push origin "vX.Y.Z"`
   - 注意：推送 `main` 不会自动触发 `ci.yml` 或 `release.yml`；推送 `v*` 标签后会自动触发 `.github/workflows/release.yml`（需配置仓库 Secret `MYGO_UPDATER_PRIVATE_KEY` 用于签名 MyGo 自动更新包与增量补丁；工作流会先校验标签与源码版本，再在远端调用 `ci.yml` 完成 `windows-latest` 与 `macos-latest` 全量离线测试、Playwright UI 回归、原生安装包 `.exe`/`.dmg` 与自动更新包构建和 `--self-test`，将附注标签的中英双语发布说明自动注入 `update-*.json` 并在 GitHub Release 正文附加直链下载指引，生成 `SHA256SUMS.txt`，最后先以草稿模式上传产物并原子发布 GitHub Release）
5. **跟进远端 GitHub Actions 并验收 GitHub Release**
   - 按 workflow（`release.yml`）、`push` 事件、标签和提交 SHA 精确匹配本次运行，不拿仓库最新运行代替本次运行
   - 持续跟踪运行与各任务状态（`Validate release metadata`、`check (windows-latest)`、`check (macos-latest)`、`Publish GitHub Release`）
   - 若任务失败，读取对应尝试的失败日志并先定位根因：
     - 若属外部环境/网络抖动等非源码原因且已确认恢复，允许对已授权的同标签同 SHA 自动执行一次 `rerun-failed`；同因再次失败则停止重跑并报告阻塞
     - 若属源码、测试或构建缺陷，不能靠直接重跑旧提交解决，也**绝不因为线上检测失败就将版本号 `+1` 另发新版本**：直接在 `main` 上修复问题并推送（保持版本清单仍锁定为当前目标版本 `X.Y.Z`），删除该版本残留的失败、草稿或不完整 GitHub Release（如有）以及远端和本地的旧 `vX.Y.Z` 标签（`git push origin :refs/tags/vX.Y.Z`、`git tag -d vX.Y.Z`），重新对修复后的提交创建附注标签 `vX.Y.Z` 并推送触发远端重新发布，直至同一个版本全部检查通过且产物完整可用
   - 工作流完成后，使用 `gh release view "v$VERSION"` 或 `scripts/github_actions.py release --tag "v$VERSION"` 核实 GitHub Release 已创建且非草稿、预发布标记正确、中英双语发布说明（上方中文、下方英文）已完整写入，且 7 个必选附件齐全且非空：
     - `ColabOne-<version>-windows-x64-setup.exe`
     - `ColabOne-<version>-macos-arm64.dmg`
     - `colabone-<version>-windows-amd64.tar.gz`
     - `colabone-<version>-darwin-arm64.tar.gz`
     - `update-windows-amd64.json`
     - `update-darwin-arm64.json`
     - `SHA256SUMS.txt`（以及前序版本存在时的 `.delta` 增量补丁）
   - 汇报时附带 Actions 运行链接、GitHub Release 链接及产物验收结果；如有部分失败、等待或未核实项需明确说明

## 发布说明格式

发布说明必须为**中英双语**，拆分为上下两个独立区块——**最上方是中文区块，下方是英文区块**，中间以水平分隔线（`---`）隔开，**不额外添加“中文 / English”之类的语言总标题**，让中英文用户都能直接阅读：

- 第一行固定为 `ColabOne vX.Y.Z`（作为附注标签标题）
- 空一行后直接输出上方中文区块，按固定顺序仅输出有内容的二级标题：`## 新增功能`、`## 修改点`、`## 修复的 bug`
- 空一行后输出水平分隔线 `---`
- 空一行后直接输出下方英文区块，按固定顺序仅输出有内容的二级标题：`## New Features`、`## Changes`、`## Bug Fixes`
- 每类下用 `- ` 列表记录，每行一句、简洁清晰，且中英文各分类的条目一一对应、语义一致（英文区块采用自然地道的英文表达，不生硬机翻）

渲染模板示例：

```markdown
ColabOne vX.Y.Z

## 新增功能
- 支持新功能示例

## 修改点
- 优化交互体验示例

## 修复的 bug
- 修复已知问题示例

---

## New Features
- Add example new feature

## Changes
- Improve example user experience

## Bug Fixes
- Fix example known issue
```

## 常用命令

在仓库根目录执行（WSL2/Linux shell 建议先设置 `PATH` 与 `LD_LIBRARY_PATH` 并重定向 `< /dev/null`）：

```bash
export PATH="$HOME/.local/bin:$PATH"
export LD_LIBRARY_PATH="$HOME/.local/sysroot/usr/lib/x86_64-linux-gnu:${LD_LIBRARY_PATH:-}"

# 1. 预览下一版本（默认最后一位 +1，不改文件）
python3 .agents/skills/colabone-release/scripts/prepare_release.py \
  --dry-run \
  --added-zh '支持新功能示例' \
  --changed-zh '优化交互体验示例' \
  --fixed-zh '修复已知问题示例' \
  --added-en 'Add example new feature' \
  --changed-en 'Improve example user experience' \
  --fixed-en 'Fix example known issue' < /dev/null

# 2. 正式准备发布（默认最后一位 +1）
python3 .agents/skills/colabone-release/scripts/prepare_release.py \
  --added-zh '支持新功能示例' \
  --changed-zh '优化交互体验示例' \
  --fixed-zh '修复已知问题示例' \
  --added-en 'Add example new feature' \
  --changed-en 'Improve example user experience' \
  --fixed-en 'Fix example known issue' < /dev/null

# 3. 快速核对三处版本清单与 diff（不在本地跑测试或 Playwright）
python3 tools/release_version.py --tag "v0.1.1" < /dev/null
git diff -- console/mygo.json frontend/package.json frontend/package-lock.json

# 4. 提交、打附注标签并推送触发远端 release.yml 全面检查与发布（RELEASE_NOTES_FILE 使用准备脚本实际输出的路径）
git add console/mygo.json frontend/package.json frontend/package-lock.json
git commit -m "chore: release 0.1.1"
git tag -a v0.1.1 -F "$RELEASE_NOTES_FILE" --cleanup=verbatim
git push origin main
git push origin v0.1.1

# （可选）仅在显式需要不打标签的远端双平台诊断时，再手动触发 ci.yml：
PREFLIGHT_SHA=$(git rev-parse HEAD)
python3 .agents/skills/colabone-release/scripts/github_actions.py dispatch \
  --workflow ci.yml --ref main --sha "$PREFLIGHT_SHA" < /dev/null
python3 .agents/skills/colabone-release/scripts/github_actions.py preflight-status \
  --branch main --sha "$PREFLIGHT_SHA" --require-green < /dev/null
```

## 参考

- 详细文件映射、校验约束与回滚说明：[references/release-checklist.md](references/release-checklist.md)
- GitHub Actions 凭据、轮询、日志读取、失败重跑与 Release 验收：[references/github-actions.md](references/github-actions.md)
