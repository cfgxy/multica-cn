# 上游同步执行日志

`docs/upstream-sync.md`（同步机制方案 v2）的执行留痕。每个批次在本文件追加一节，
记录窗口基线、簇级结论、迁移编号处置、冲突裁决与验证证据。机制与闸门定义见方案，
本文件只记事实。

## 批次一：机制落仓 + 457/458 让号 + 试点 38 条（RUYI-668）

- 执行日期：2026-10-10
- 分支：`agent/agent-f70e39b85abd/ruyi-668-f291b1e6b435`
- 窗口基线：fork `d222b54ce399eab653dff36a314c554b27d1f0a2`（2026-10-10 21:59:37 +0800，
  origin 集成分支 tip）→ upstream `c8d6a1fad03d20a020ca90d1e50257d9f060ee87`
  （2026-10-10 17:03:32 +0800）
- 吸收提交数：38（安全 3 + CLI 13 + desktop 6 + selfhost 3 + test 13）
- 累计 delta（分支累计，含 docs、让号重编、闸门与三笔修复提交）：383 files
  changed，+11132 / −8342

### 提交清单（簇间可区分）

| Commit | 内容 |
| --- | --- |
| `132d49dadeced6e5215cc60757df7369a4493ee7` | `docs/upstream-sync.md` 方案 v2 附件全文落仓 |
| `6be194603c538af3254a68c942b47c311826c16a` | 457/458 让号重编至 945/946（内容零变更，见下） |
| `eb4727ac8275a448ac730bae46d046919289df04` | C-security：Next.js CVE-2026-75604、GHSA-vcvr-r3jv-pc5j、Go 1.26.9 工具链安全地板（3 条） |
| `55069ebbf01b027b650e3e1859e0db8214cd6f10` | C-cli：CLI 簇 13 条 |
| `d87392f97666c30d77443210b72f9251e269d702` | C-desktop：标签编号快捷键、导航历史菜单、窗口工具栏对齐（6 条） |
| `fb3f1594a12b997f60eeb65f4691aa9f59e80fc1` | C-selfhost：web 镜像 OpenSSL 升级、Telegram/DingTalk 密钥透传（3 条） |
| `73d85cf4be97705b10c14336182266218cf4372a` | C-test：测试簇 13 条（294 files，+3537 / −7806） |
| `86cc29139ae0775106dc616ce4e99b5240ae57ce` | 方案 v2 §3.3 三条机械断言脚本化（`server/cmd/migrate/upstream_sync_mechanical_gates_test.go`） |
| `447278afd742d32e58e89c37407d806243a38139` | 吸收测试 `App.auth-recovery` 补齐 fork mock 面（全量验证发现，见冲突裁决） |
| `7f8fd7029fd0815e04813978ef57ba237b78d789` | 本执行日志批次一节落仓 |
| `2024dcefd8d0c5a9caae4b8fcca47363ec8f4e2e` | 锁等待白名单补 `advisory` 事件，对齐上游 helper（全量验证发现，见冲突裁决） |

### 迁移编号处置：457/458 → 945/946

上游同号段撞号（上游窗口内出现 `457_plugin_public_listing`、
`458_plugin_package_public_index`），fork 侧让号，重编映射：

| 旧 stem（fork 已应用） | 新 stem |
| --- | --- |
| `457_plugin_public_listing` | `945_plugin_public_listing` |
| `458_plugin_package_public_index` | `946_plugin_package_public_index` |

- 重编为纯改名：文件内容零变更，`git diff` 仅路径。
- 已应用环境须同步改写账本（`applied_at` 保留原值）：
  `UPDATE schema_migrations SET version = '945_plugin_public_listing' WHERE version = '457_plugin_public_listing';`
  `UPDATE schema_migrations SET version = '946_plugin_package_public_index' WHERE version = '458_plugin_package_public_index';`
- 槽位库证据（dev1 全新重建）：账本含 `945_plugin_public_listing`、
  `946_plugin_package_public_index`，无任何 457/458 残留行（fresh apply，无需改写）。
- 共享主库 `multica`（本地）与 hp-server 生产主库已按旧号应用：**改写须 Owner 授权，
  本批次未执行**。未改写前这两库不得再执行 `migrate up`（ migrator 会按新 stem 重放
  DDL，457/458 的对象已存在而失败；先授权改写、后迁移）。
- 零残留核对：旧 stem 活引用（消费旧号的代码/迁移/登记）0 处；全仓仅两处声明性
  命中——闸门测试的「旧→新」重编映射数据与 `docs/` 方案/日志的映射语境，均非活引用。
- 登记簿：主检出 `.multica/ALLOCATIONS.md` 945/946 行 `reserved`，分支已校正为本分支。

### §3.3 三条机械断言（脚本化）

`server/cmd/migrate/upstream_sync_mechanical_gates_test.go`，随 `go test ./cmd/migrate/`
常驻执行（本批次头部已 PASS）：

1. **来源可辨**：fork 自有 stem 全部 ∈ 9xx；被吸收 stem 在 450–899 带内原样存在且不与
   fork 自有/重编 stem 交叠。
2. **三源协调**：磁盘迁移面 × ALLOCATIONS.md 登记行 ×（测试内）9xx 账本口径一致；
   945/946 行 stem 与状态（reserved/applied）齐备。
3. **覆盖完备**：9xx 磁盘 stem ⊆ 直通 stem ∪ 合并组目标 ∪ 合并后 stem ∪ 已申报批次
   重编 stem——被吸收批次的 stem 必须先在批次数据中申报才允许存在。

### 簇级结论与冲突裁决

裁决总则（方案 §2.3）：上游语义优先；fork 自有功能与治理文件保留；上游测试钉住
fork 未吸收的生产架构时，测试随宿主簇顺延至后续批次。

- **C-security（3 条）**：Next.js 15.5.7 安全版、GHSA-vcvr-r3jv-pc5j 依赖升级、Go
  1.26.9 工具链地板。纯版本/配置面，无冲突。
- **C-cli（13 条）**：CLI 命令面吸收。`cli.fr.mdx` 法语文档引用 fork 未吸收的命令面，
  顺延宿主簇（batch-2）。
- **C-desktop（6 条）**：标签编号快捷键、导航历史菜单、窗口工具栏。`desktop-layout.tsx`
  import 冲突取并集（`ServerSwitcherDialogs` 为 fork RUYI-59 面貌、`WindowToolbar`
  为上游新件，二者共存）。吸收的全 App 测试 `App.auth-recovery.test.tsx` 依赖上游 App
  无 RUYI-59 接线的 mock 面，在 fork 上按本仓测试惯例补 `defaultStorage` mock 并 stub
  `./platform/desktop-servers`（`447278afd7`），测试语义不变——fork 接线自有专属测试。
- **C-selfhost（3 条）**：compose 面吸收，DU 冲突以 `git cat-file -e HEAD:<path>`
  判定后 `git rm` 跟随上游删除。
- **C-test（13 条）**：测试簇吸收与顺延是本簇主要工作：
  - codex raw agent-message 回调架构测试（6 个用例 + 聚合常量）：宿主生产代码在
    batch-2 C-agent 簇，测试顺延。
  - channel Router `SenderChannelID` 锚定测试：钉住 batch-2 C-im 的生产字段，随簇顺延。
  - hermes follows-task-home（GH #8310）测试：fork 生产实现硬编码 `~/.hermes/skills`，
    宿主生产簇未吸收，测试顺延。
  - handler clock-skew 界（`maxTaskMessageClockSkew`）测试：宿主 C-server 簇顺延。
  - `execenv/local_worktree_test.go`：随上游 81f0fb41 模板架构移植（含
    gc.auto/maintenance.auto 关闭的 template 修复本体）。
  - `mermaid-diagram.test.tsx`：上游删除 `readFileSync` import 是因其选择测试改写；
    fork RUYI-80 测试仍依赖，保留 import。
  - `.gitignore` 合并：fork 证据忽略块保留，追加上游 `perf-report/`。
  - **helper 白名单消解失误（`2024dcefd8` 修复）**：吸收的竞态测试确定性加固
    （autopilot 订阅保存 / subtree 退订 `LosesToConcurrentRevoke`）依赖
    `waitForWaiterBlockedBy` 识别 advisory 锁等待，上游 helper 白名单含
    `'advisory'` 而 fork 侧旧版仅 `transactionid`/`tuple`；冲突消解保留了
    fork helper，helper 结构性看不到 `pg_advisory_xact_lock` 等待，两个测试
    确定性误报 never-blocked。按上游补事件后即过——教训：吸收测试与其测试
    基础设施（helper/mock 面）必须同簇整体吸收，不得按 hunk 拆开取舍。

### 顺延登记（batch-2 钩稽清单）

| 顺延项 | 宿主簇 | 原因 |
| --- | --- | --- |
| codex 聚合常量 + 6 回调测试 | C-agent | 生产回调架构未吸收 |
| `router_delivery_anchor_db_test.go` | C-im | 钉住 batch-2 `SenderChannelID` 生产字段 |
| hermes follows-task-home | 宿主生产簇 | fork 无 HERMES_HOME 跟随实现 |
| handler clock-skew 测试 | C-server | 生产界未吸收 |
| `TestServerErrorCode` 等服务器测试残留 | C-server | 同上 |
| `cli.fr.mdx` | CLI 宿主簇 | 命令面前置未吸收 |
| wakeup 公告、平台 skill 树等非代码面 | 对应簇 | 非本批试点范围 |

### 验证记录

- 定向测试：`pkg/agent`、`pkg/llm`、`pkg/taskfailure`、`pkg/featureflag`、
  `internal/featureflags`、`internal/daemon/execenv`、`cmd/migrate` 全套、
  `internal/daemon`（local-skills）PASS；desktop 209 + core 2103 + views 5366/5368 +
  web/desktop 目标 TS 测试 PASS。
- 全仓 `go vet` 干净；sqlc 生成物 `git status --porcelain server/pkg/db/generated`
  0 脏（重编为改名，schema 输入未动；注：环境无法出网到 proxy.golang.org，
  `sqlc generate` 全量重跑受限，以输入未变 + 生成物零 diff 为证）。
- 迁移冒烟：dev1 槽位库 fresh 全量应用 519 个迁移成功（含 945/946），并发迁移
  单测 PASS。
- 全量 `make check`（ENV_FILE 指向 dev1 槽位 env）：首跑暴露 `App.auth-recovery`
  3 例失败 → 归因本批吸收测试 mock 面（非预存红）→ `447278afd7` 修复；复跑
  desktop 全绿（697+3 通过），唯一残留失败为 views `agent-detail-page.test.tsx`
  「hides the more-actions trigger」1 例——该测试与其被测文件在本批 382 文件
  delta 之外、基线内容一致，且在吸收前 HEAD 确定性复现 4/4，定性为**基线预存红**
  （不在既有红口径清单，本单登记），不属本批引入。TS 阶段（[1/5][2/5]）两跑
  证据齐备。[3/5] Go 全套首跑 82 包 ok、仅 handler 包 2 例失败（上述 helper
  白名单误报）→ `2024dcefd8` 修复 → 复跑全套通过（exit 0，含 handler 包）；
  `migrate up` 对 dev1 幂等 skip 至 945/946 后 Done。[5/5] Playwright E2E
  对 dev1：首跑 34/45——8 例失败为 web dev server 触发槽位看门狗
  `RESOURCE_KILL_WEB`（RSS 9498MB > 8192MB 配额，登记在案的保护行为）后的
  连接拒绝级联；重启补跑 11 例又 7 过，残留 6 例失败全部落在本批 delta
  之外（被测生产代码与 spec 均与基线同源）：issue-table:174 为 dev1 库
  多轮测试数据污染（分组断言多出 30 行）、navigation:12 为 dev 模式下
  逐路由标题（MUL-6222）不生效、quick-actions 为空库无 agent fixture、
  ruyi483-race 为计时敏感探针超时、settings×2 为第二次 RESOURCE_KILL_WEB
  （15:45:41Z）级联。合计 41/45 通过；残留失败均属已登记红口径家族
  （空库无 fixture / E2E 超时型 / 看门狗保护），无本批引入的新失败。
  seeded fixture 层仍为后续单候选。

### 遗留与授权项

1. 共享主库 `multica` 与 hp-server 生产主库的 457/458→945/946 账本改写：待 Owner
   授权后按上文 SQL 执行（改写前禁 `migrate up`）。
2. 顺延登记表所列各项随 batch-2/3 宿主簇落地。
3. `AGENTS.md` 为平台注入改写、两份 RUYI-656 对账/方案为工作副本，均不纳入本批提交。
