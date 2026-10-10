# Web E2E 验收链路 Runbook（RUYI-632）

本目录的 Playwright 套件（`pnpm exec playwright test`，45 例，`workers=1`）要形成**业务 PASS/FAIL 结论**，web 入口必须可追溯。2026-10-10 QA 轮 4（RUYI-593）的 45 例 E2E 因入口是源码 dev server 被判 `ENV-FAULT`：28 过 17 败无法归因。本 Runbook 定义治理后的验收链路：可核验产物入口、开跑前预检、预算/负载方法学与症状三分归因协议。

## 1. 入口形态：release entry（验收标准 1）

默认的槽位 web 组件是 `next dev`（源码 dev server）。浏览器验收一律使用**生产构建入口**：

```bash
# 0) 租槽位并装载被测 SHA（写命令自报归属）
MULTICA_CALLER_OWNER=<issue-id> scripts/dev-env.sh dev1 use ARGS=<被测40位SHA>
MULTICA_CALLER_OWNER=<issue-id> scripts/dev-env.sh dev1 up --components api

# 1) 生产构建 + 产物清单（在槽位绑定的 worktree 内构建）
bash scripts/e2e-release-entry.sh dev1 build

# 2) 依赖预检（见第 3 节）
bash scripts/e2e-preflight.sh dev1

# 3) 以 release 模式启动 web（替代 dev server，同槽位同看门狗管理）
MULTICA_CALLER_OWNER=<issue-id> MULTICA_WEB_MODE=release \
  scripts/dev-env.sh dev1 up --components api,web

# 4) 机械核验产物（QA 回报直接引用输出行）
bash scripts/e2e-release-entry.sh dev1 verify

# 5) 跑套件（从槽位 worktree 或被测载体均可，入口指向槽位 web）
PLAYWRIGHT_BASE_URL=http://localhost:13801 pnpm exec playwright test
```

### manifest 与核对

`build` 在槽位 worktree 写 `e2e-artifacts/`（已 gitignore）：

- `manifest.json` —— `source.git_sha`（被测完整 40 位 SHA）、`source.git_branch`、`source.worktree_path`、`build.build_id`、`build.built_at`、`build.env_file_sha256`、`build.next_public_bake`（构建时烘焙的 `NEXT_PUBLIC_*`）、`artifact.sha256`（产物聚合摘要）、`artifact.file_count/total_bytes`。
- `file-hashes.txt` —— 产物逐文件 `<sha256>  <相对路径>` 清单（`apps/web/.next` 全部文件，排除 `.next/cache`；LC_ALL=C 排序）。聚合摘要 = 该文件内容的 SHA256。

`verify` 把当前产物重哈希到临时副本与 manifest 比对（不覆写证据文件），核对三项：产物 sha256、`BUILD_ID`、worktree HEAD。QA 回报引用格式：

> release entry git `<40位SHA>`，artifact sha256 `<聚合摘要>`（`scripts/e2e-release-entry.sh dev1 verify` PASS）

任何人可在同一槽位 worktree 复跑 `verify` 独立复核。构建前脚本强制 dirty 检查：除平台注入豁免文件（`AGENTS.md`）外，存在未提交 tracked 改动即拒绝构建——产物必须可追溯到 commit。

## 2. 预算/负载方法学（验收标准 2）

**为什么 dev server 会被击杀**：`next dev --webpack` 常驻模块图与 HMR 缓存，45 例串行长跑实测 RSS 8741 MB，超过槽位 web 预算 8192 MB（`slots.json` `resource_budget`），看门狗连续 2 拍越界后按设计击杀，连带 9 例连接拒绝。node 的 `--max-old-space-size` 自限不约束 RSS（堆外缓冲计入 RSS），预算不能靠调大解决——那是生产预算基线，本单不改。

**方法学**：浏览器验收窗口一律使用 release entry。生产 `next start` 无 HMR/模块图常驻，RSS 稳定在远低于预算的水平（首轮实测见本单归因报告），看门狗照常守护但不会击杀。**不改预算，改入口**。

补充负载纪律：

- `workers=1` 串行是套件默认，不要并行加大 workers（槽位 4 核钉扎，共享宿主机）。
- 禁止任何真实压测（GTI 资源红线）：资源护栏验证只用配置静态断言与桩进程。
- 看门狗击收取证：宣布组件稳定或回收结论前查 `manifest.env` 的 `RESOURCE_KILL_*` 章与 `resource-events.log`（见 multica-handbook〈env-ops〉）。
- 长跑窗口若必须使用 dev server（如调试），预期 RSS 会持续增长，不要用于验收结论。

## 3. QA 开跑前依赖预检（验收标准 3）

`bash scripts/e2e-preflight.sh dev1`（只读探针，QA 回报直接粘贴输出）：

| 检查 | 判定 | 归因意义 |
| --- | --- | --- |
| api health | `/health` 200 且 commit == 槽位绑定 SHA | 不一致 = 测的不是目标 SHA |
| web entry | 槽位 web 端口应答 | 组件没起 = 后续全挂 |
| release entry | 产物 digest 与 manifest 一致 | 无 manifest = 入口不可追溯，结果无业务资格 |
| redis | `REDIS_URL` 端口可达 | 不可达 → API 记 DB fallback（日志 `WARN`/`WRN` 双前缀取证），realtime 路径症状不能干净归因 |
| postgres | `DATABASE_URL` 端口可达 | 不可达 = E2E 登录/seed 全挂 |
| feature flags | 经 web 入口 `GET /api/config` 实测 | `composio_mcp_apps=false` 时 `e2e/agent-mcp.spec.ts` 的「creator sees the MCP Apps tab」用例按配置必败——要么给 api 进程设 `FF_COMPOSIO_MCP_APPS=true`，要么把这类失败显式归因为装置配置 |
| upload route | 直连 api 与经 web 代理各 `POST /api/upload-file`（不带鉴权）应答 401/400/403 | Go handler 本体永不 404；404 = 请求死在 web 运行时改写或 API base 指错——QA 轮 4 的 chat-attachments 失败即此类 |
| port ownership | api/web 端口有监听者 | 无监听 = 组件未起或不属本槽位 |

退出码：0 = 无 FAIL（WARN 可开跑但必须记录进报告）；1 = 有 FAIL（先修复再跑）。

### E2E 进程环境确定性（RUYI-632 新增）

E2E harness 的 API base 链是 `e2e/env.ts` 读 `<载体根>/.env.worktree` → `e2e/fixtures.ts` 的 `NEXT_PUBLIC_API_URL || localhost:${PORT||8080}`。槽位 `use` 现在会把槽位 env 物化为槽位 worktree 内的 `.env.worktree`（gitignored，600 权限，每次 use 刷新），把 API base 钉死到槽位后端端口——QA 轮 4 的载体里没有这个文件，base 依赖发起 shell 的导出，属于不可追溯的环境 luck。跑套件的 shell 应保持干净（`env -u NEXT_PUBLIC_API_URL -u PORT -u PLAYWRIGHT_BASE_URL pnpm exec playwright test` 或显式 `PLAYWRIGHT_BASE_URL=http://localhost:<槽位web端口>`），禁止让残留导出覆盖 `.env.worktree`（dotenv 不覆盖已存在变量）。

## 4. 症状三分归因协议（验收标准 4）

release entry 上复跑后，每个失败症状按且仅按以下三类归因：

- **存量**：main 基线 + 可核验入口上稳定复现 → 与入口/环境无关的既有缺陷，登记候选 Issue 分流，不在链路单内修复。
- **回归**：main 上不复现、仅在含某 delta 的分支上复现 → 归属该 delta 的单，回传其 Issue。
- **环境**：release entry 上不复现，或根因为装置配置（flag 缺失、Redis 缺失、dev server 特有行为）→ 归因记录 + 配置修正/装置修正，不升级为业务缺陷。

交叉比对义务：归因涉及 Comments 编辑器、错误呈现/i18n 时与 RUYI-561 改动面比对；涉及决策卡 UI 时与 RUYI-620 改动面比对，避免重复修复与撞分支。

## 5. 清理

- E2E 合成工作区（`e2e-workspace-*`/`e2e-mcp-*`）：正式 `DELETE /api/workspaces/{id}` 逐个清理并复查库内残留为 0（QA 轮 4 先例：19 个全部 204）。
- 槽位：qa 相位验证结束 `MULTICA_CALLER_OWNER=<issue-id> scripts/dev-env.sh dev1 lock-release` 即释归池；`down` 停组件。
- 收尾清点：`MULTICA_CALLER_OWNER=<issue-id> bash scripts/qa-clean.sh --issue <issue-id> --yes`。
