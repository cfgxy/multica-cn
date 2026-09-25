# ADR-001（RUYI-179）四级 Prompt 版本管理与「自进化」质量评估架构

- 作者：蔡小星（架构师/Leader）｜日期：2026-09-24｜状态：**待 Owner 采纳**
- 输入：Issue RUYI-179 描述（Owner 原始需求 2026-09-24 23:08）；谢小婷《RUYI-179 产品需求规格 v1.0》（评论 `01a0d407`）；本文档全部现状事实由本 run 在 worktree `31977c5af38aa7cc3a39195a7f1159016c1cb86b` 上只读核查取得。
- 口径：【事实】= 已核查到代码/迁移出处；【决策】= 本 ADR 拍板项；【待 Owner】= 需 Owner 采纳后方可实施。

## 1. 背景

Owner 要求把当前靠外部 git 仓（`snapshot.sh` / `check_prompts.py` / `restore.sh`）维护的四级 Prompt 纳入 Multica 底座版本管理，并接入 Langfuse 做后置实测质量评估，以「自进化」入口呈现看板。

产品口径已由 PM 规格收敛，本 ADR 只解决「怎么建」：数据模型、版本归属埋点、指标计算位置、Langfuse 集成边界、门闸落位、三端呈现边界。

## 2. 现状事实基线（只读核查结论）

### 2.1 四级 Prompt 的实际存储【事实】

四级 prompt 目前是**四张既有业务表上的四个文本列**，没有任何版本历史字段：

| 层级 | 存储位置 | 出处 |
| --- | --- | --- |
| 空间 context | `workspace.context TEXT` | `server/migrations/006_workspace_context.up.sql` |
| 项目 instructions | `project.instructions TEXT` | `server/migrations/901_project_instructions.up.sql` |
| 小队 instructions | `squad.instructions TEXT NOT NULL DEFAULT ''` | `server/migrations/088_squad_instructions.up.sql` |
| 智能体提示词 | `agent.instructions TEXT NOT NULL DEFAULT ''` | `server/migrations/021_agent_instructions.up.sql` |

**规格 §1.4 的第一条假设由此关闭**：平台当前**不保留**任何 prompt 历史版本或审计字段，本单是从零建版本层，不存在迁移既有历史的工作量。

### 2.2 注入链路【事实】

- 组装点唯一：`server/internal/handler/daemon.go` 的 task claim handler 读 `workspace.context`（`daemon.go:3133`）、project instructions（`project_resource.go:889`）、squad briefing（`squad_briefing.go:187`）、agent instructions，下发给 daemon。
- 渲染点唯一：`server/internal/daemon/execenv/runtime_config_sections.go`（`writeWorkspaceContext` / `writeProjectInstructions` / `AgentInstructions`）。
- **关键结论**：四级 prompt 在 **claim 这一个时刻**被解析成一份 brief 快照下发。这正是「run 版本归属埋点」的天然落点——claim 时已持有四级全部内容，落版本 ID 不需要任何额外查询。规格 §3.3「进行中 run 保持启动时版本」由现有架构天然成立，不需新增机制。

### 2.3 可直接复用的既有资产【事实】

1. **`promptscan` 包**：已有带 revision 的 prompt 秘密扫描器（`marketplace_prompt.go:678` 调用 `promptscan.Scan`，结果含 `Revision` / `Findings`，findings 只含 category/rule/line，匹配文本不落库）。规格 §3.6 的「凭据模式检查」直接复用，**不新写扫描器**。
2. **`marketplace_prompt_version` 表**（迁移 914）：已验证过的 prompt 快照建模范式——`series_id` 分组 + `version INT` + `content_sha256` + `scanner_revision` + `scan_result`，且已确立「无外键、发布行不可变、新版本即新行」的house rule 落地样板。本单照此范式建表，**但不复用该表**（见 §4.1 决策理由）。
3. **run 级数据**：`task_usage`（token 计费列 + `context_tokens` / `turns` / `compactions` / `max_context_tokens`，迁移 908/915）、`task_message`（`type` / `tool` / `input JSONB` / `output` / `seq`，迁移 026，由 daemon 批量上报 `daemon.go:4728`）。
   - **规格 §1.4 第二条假设结论**：run 级 token **已完整采集**；工具调用序列**已完整采集**（`task_message.tool` + `input`），困惑度的「同参数重复工具调用」可直接从 `task_message` 计算，不需要新增采集埋点。这是本单最重要的技术事实——质量体系的数据底座已经存在。
4. **看板范式**：`task_usage_daily` / `task_usage_hourly` / `*_dirty` / `*_rollup_state`（迁移 073/077/084/101）已有成熟的「脏标记 + 增量 rollup」体系，自进化看板的聚合层照此范式，不新造聚合机制。
5. **侧栏导航**：`packages/views/layout/app-sidebar.tsx:147` 的 `workspaceNav` 数组，`agents` 之后即 `squads`；路由与图标在 `packages/core/paths/paths.ts` 与 `route-icons.ts` 集中声明。Owner 要求的「智能体下方新入口」= 在 `workspaceNav` 的 `agents` 项后插一项，三端自动一致（图标由路径派生）。

### 2.4 Langfuse 现状【事实】

仓库内 `server` / `packages` / `apps` 全域**无任何** langfuse / otlp / opentelemetry 引用。本单是 Langfuse 接入的第一跳。

## 3. 架构原则（本单的四条自我约束）

1. **不新造采集**：质量指标全部从既有 `task_usage` / `task_message` / issue 事实派生。P0 新增的唯一埋点是 run 的四级版本归属。
2. **新增实体不超过 2 个**（过度设计预警线）：P0 新建 `prompt_version` 与 `prompt_quality_daily` 两张表，其余全部是既有表加列。
3. **版本层不侵入注入链路**：`workspace.context` 等四个业务列保持为「当前生效内容」的唯一读取点，注入链路零改动；版本表只做历史与审计。这条是本 ADR 最关键的取舍（§4.2）。
4. **Langfuse 是导出目标，不是依赖**：Langfuse 不可用时，版本管理与看板全功能可用。

## 4. 决策

### 4.1【决策】单表四级：`prompt_version`

一张表承载四级，`scope` 列区分层级，`scope_id` 指向对应实体：

```
prompt_version
  id UUID PK
  workspace_id UUID NOT NULL                -- 全查询按 workspace 过滤（house rule）
  scope TEXT NOT NULL CHECK (scope IN ('workspace','project','squad','agent'))
  scope_id UUID NOT NULL                    -- workspace/project/squad/agent 的 id
  version INT NOT NULL                      -- 同 (scope, scope_id) 内单调递增，从 1 起
  content TEXT NOT NULL DEFAULT ''
  content_sha256 TEXT NOT NULL
  source TEXT NOT NULL CHECK (source IN ('import','edit','revert','auto_snapshot'))
  source_version INT                         -- source='revert' 时记来源版本号
  change_note TEXT NOT NULL DEFAULT ''
  scanner_revision TEXT NOT NULL DEFAULT ''  -- 复用 promptscan.Revision
  gate_result JSONB NOT NULL DEFAULT '[]'    -- 门闸 findings：category/rule/line，不含匹配文本
  author_user_id UUID
  author_note_issue_id UUID                  -- 授权出处（Issue 引用），规格 §3.6
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
  UNIQUE (scope, scope_id, version)
```

**为什么单表而非四表**：四级的版本语义完全同构（整体快照、只追加、copy-forward 回滚），四张表会把同一套 CRUD、门闸、diff、台账查询写四遍。`scope` 是判别列而非多态外键——house rule 禁外键，关系在应用层解析，`scope + scope_id` 的组合校验在写路径内完成。

**为什么不复用 `marketplace_prompt_version`**：那张表的语义是「跨 workspace 的可发布市场资产」，带 `visibility` / `publisher` / `license` / `withdrawn` 与 D4 的跨空间泄露约束；本单是「workspace 内部治理版本线」。合并会把两套权限模型压进一张表的 CHECK 里——正是 RUYI-100 当初拒绝把 prompt 塞进泛化 `MarketplaceItem` 的同一个理由。范式复用，表不复用。

**索引**（各自单独一个迁移文件，`CREATE INDEX CONCURRENTLY`；本表为新建表，按迁移 914 已确立的先例，建表同文件内的索引可非并发建）：
- `(scope, scope_id, version DESC)` — 台账与最新版本
- `(workspace_id, created_at DESC)` — 看板「版本变更事件流」

### 4.2【决策】生效内容仍读业务列，版本表只做历史

保存一个新版本 = **一个事务内两次写**：业务列（`workspace.context` 等）更新为新内容 + `prompt_version` 插入一行。当前生效版本 = 该 `(scope, scope_id)` 的最大 `version`。

**替代方案与否决理由**：
- 方案 B「业务列改存 `current_version_id` 指针，内容只在版本表」：注入链路（claim handler 四处读取）全部要改成 join 版本表，且 `squad_briefing.go` / `project_resource.go` / `daemon.go` 三个独立读取点都要动。收益仅是「少一份内容副本」，代价是改动注入热路径——违反原则 3。**否决**。
- 一致性风险（业务列与版本表不同步）由「同一事务 + `content_sha256` 可随时校验」兜住；看板提供一致性自检查询（业务列 sha 与最大版本 sha 比对），不一致即告警。

规格 §3.3「指认历史版本 = 生成内容等于 vK 的新版本」在本方案下天然成立：所有生效变更都是同一条写路径，审计链 = 版本线本身，无需额外事件流表。

### 4.3【决策】run 版本归属：`agent_task_queue` 加一个 JSONB 列

```
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS prompt_versions JSONB NOT NULL DEFAULT '{}'::jsonb;
-- {"workspace":{"id":"<uuid>","version":12},"project":{...},"squad":{...},"agent":{...}}
-- 该层级未参与本次注入时，对应键缺席（而非 version:0）——「未注入」与「注入了 v0」必须可区分。
```

写入点：task claim handler，在已读取四级内容的同一处顺带落版本号，**零额外查询**。

**为什么不新建 `task_prompt_version` 表**：一个 task 对四级版本是 1:1 关系，拆表只为一行数据引入一次 join，且 `agent_task_queue` 已有 `context JSONB` 的同类先例（迁移 003）。JSONB 而非 4 个 UUID 列：层级可能缺席（无项目绑定的 run 无 project 层），JSONB 的键缺席比 4 个 NULL 列语义更准。

**指标关联查询**：`agent_task_queue.prompt_versions` → `task_usage.task_id` / `task_message.task_id` 均按 `task_id` 关联，无需改这两张表。

**规格 §2.1「无此项整个质量体系不成立」成立**：这是 P0 的第一优先实现项，且必须先于任何指标开发落地——埋点上线日即数据起点（规格 §5.4 / Q5）。

### 4.4【决策】质量指标：派生计算 + 日聚合，不存原始分

```
prompt_quality_daily
  id UUID PK
  workspace_id UUID NOT NULL
  scope TEXT NOT NULL
  scope_id UUID NOT NULL
  version INT NOT NULL              -- 该日归属的 prompt 版本
  day DATE NOT NULL
  finished_runs INT NOT NULL DEFAULT 0
  -- Token（规格 §4.4）
  injected_tokens BIGINT            -- 前置：版本保存时算一次，冗余在此便于看板单查
  run_tokens_median BIGINT
  -- 困惑度（§4.3，执行困惑度）
  perplexity_score_median NUMERIC
  repeated_tool_calls INT NOT NULL DEFAULT 0
  -- 纪律性（§4.2）
  discipline_score_median NUMERIC
  discipline_deductions JSONB NOT NULL DEFAULT '[]'
  -- 达成率（§4.5）
  first_pass_runs INT NOT NULL DEFAULT 0
  UNIQUE (scope, scope_id, version, day)
```

- 计算位置：**服务端 rollup job**，照 `task_usage_daily` 的脏标记 + 增量范式（迁移 073/077）。不在读路径实时算——看板查询必须是单表扫描。
- 原始事实不复制：扣分明细 `discipline_deductions` 只存 `{run_id, rule, points, source}`，证据回溯靠 `task_id` 反查 `task_message`。
- 「版本生效期」窗口（规格 §5.2）由 `prompt_version.created_at` 前后相邻两行界定，不存冗余区间列。
- **纪律性人工标注**：P0 只做「标注入口 + 落库」，标注行存在 `discipline_deductions` 内，不为它新建表（规格 §4.2 的人工标注量级是抽样与争议件，非高频写入）。

### 4.5【决策】立法门闸：服务端内置，规则二分

保存前强制校验，不通过则事务回滚、不产生版本（规格 §3.6 / 验收 W2）。规则分两类：

1. **凭据与秘密**：直接调用既有 `promptscan.Scan`，`scanner_revision` 与 `gate_result` 落 `prompt_version` 行——与 marketplace 完全同一套扫描器，同一套「pass 意味着本 revision 无规则命中，而非不含秘密」的语义。
2. **结构与文风**：`tools/prompt-structure.json` 与 `check_prompts.py` 的**可机械判定子集**移植为 Go 校验器。
   - **明确取舍**：`check_prompts.py` 的规则里，凡依赖人工判断的（层次归属是否得当、代价是否值得）**不移植**——门闸只做机械项，判断项留 Owner 修法权。移植清单与逐条对照表是实现阶段的交付物之一，不在本 ADR 内清点。
   - 门闸规则集本身的版本可追溯：列 P1（规格 §3.6 同结论）。

### 4.6【决策】Langfuse 集成边界：单向导出，不进读路径

- **推送内容**：run 级 trace = `{task_id, agent_id, workspace_id, prompt_versions(四级版本 ID 与版本号), token 用量, turns/compactions, 派生质量分, 达成率标记}`。
- **prompt 正文不出平台**（规格 §3.8 / Q8 推荐 A）：Langfuse 侧用版本 ID + `content_sha256` 关联，正文留在平台库。
- **方向单向**：Multica → Langfuse 导出；看板数据源是平台自己的 `prompt_quality_daily`，**不从 Langfuse 回读**。理由：看板是交付的核心验收面，不能让外部服务可用性成为它的前置依赖；Langfuse 承担的是「深度下钻与跨 run 探索」，是增益不是底座。
- **协议**：OTLP/HTTP（Langfuse 原生支持），实现为服务端可关闭的导出器；未配置时全功能可用，配置错误只产告警不阻断 run。
- **凭据**：走既有环境变量与配置体系，本 ADR 不含任何连接串与密钥。
- **与 RUYI-177 的关系**：RUYI-177 正在为 CLIProxyAPI 做 OpenTelemetry/OTLP 接 Langfuse。**两者不共用代码但必须共用 Langfuse 实例与 trace 命名口径**——RUYI-177 采的是模型调用层 span，本单采的是 run 业务层 trace。实现阶段需与 RUYI-177 对齐 trace/span 命名与 `task_id` 关联键，否则 Langfuse 上两套数据无法拼接。这是本单唯一的跨单技术依赖，已登记为实现阶段的对齐项。

### 4.7【决策】前端落位

- 路由：`paths.ts` 新增 `selfEvolve: () => \`${ws}/self-evolve\``；`route-icons.ts` 注册 segment 与 navKey；`app-sidebar.tsx` 的 `workspaceNav` 在 `agents` 后插入 —— 侧栏与桌面 tab bar 图标自动一致（现有机制，`app-sidebar.tsx:141` 注释已声明）。
- 页面：`packages/views/self-evolve/`（共享业务视图），web 与 desktop 各做平台 wiring；**P0 仅要求 Web 端达标**（规格 §8 同结论），桌面端路由注册同期做（成本极低），移动端不做。
- 数据层：`packages/core/self-evolve/` 的 queries + zod schema，按 `parseWithFallback` 规则解析（CLAUDE.md API 兼容性条款）。

### 4.8【决策】外部 prompt 仓的迁移路径（依赖 Owner 拍板 Q3）

技术侧结论：**一次性导入 + 只读导出**在实现上最省，且有逐字节校验兜底（`content_sha256` 对比）。双写过渡（Q3 选项 B）需要一个双向同步器，是本单工作量最大的单点且长期维护为负资产——技术上不推荐。此项产品影响归 Owner（改变 Owner 现有工作流），见 §6 Q3。

## 5. 实施分期与工作量

沿用规格 §2 的三期切分，架构侧补工作量估算与强制次序：

**P0（本单）——强制次序，不可并行**

1. `prompt_version` 表 + 写路径（保存/回滚/导入）+ 门闸内置 + 权限校验 ≈ 3 人日
2. run 版本归属埋点（`prompt_versions` JSONB + claim handler）≈ 0.5 人日 —— **必须最先上线**，它决定数据起点
3. 存量四级 prompt 导入为 v1 基线 ≈ 0.5 人日
4. `prompt_quality_daily` + rollup job（Token 前置/后置 + 困惑度规则项 + 达成率）≈ 2.5 人日
5. 「自进化」入口 + 版本台账 + diff + Token 趋势 + 数据起点声明（Web）≈ 3 人日
6. Langfuse 导出器（OTLP/HTTP，可关闭）≈ 1.5 人日

P0 合计 ≈ 11 人日。**超过 3 人日的架构级变化，故本 ADR 须 Owner 采纳后方进实现**（workspace context 立法门闸与我的角色条款双重要求）。

**P1**：纪律性完整呈现与人工标注闭环、版本前后对比（含样本可信度标签）、防回归测验（手动触发）、门闸规则集版本化、只读导出、桌面端适配。

**P2**：AI 优化建议草案（仅写入 Issue 提案）、测验周期定时触发。

**全期不做**：AI 自动改写并切换 prompt（触碰 Owner 修法权）、跨 workspace 质量基准对比、skills/handbook 资产版本管理。

## 6. 需 Owner 拍板的决策项

PM 规格 §7 的 Q1–Q8 我逐条复核，架构侧结论如下（未复述选项全文，见规格 §7）：

| # | 决策点 | 架构侧复核 | 推荐 |
| --- | --- | --- | --- |
| Q1 | 困惑度口径 | 执行困惑度的数据已在 `task_message` 内完整可算（§2.3），零新增采集；perplexity 需模型侧统计管线，当前无任何 OTel 基建 | **A 执行困惑度** |
| Q2 | 分期节奏 | P0 已 11 人日，一次全做无验收锚点 | **A 三期** |
| Q3 | 外部 prompt 仓去留 | 双写过渡需一个双向同步器，是最大单点工作量且长期为负资产；一次性导入有 sha256 逐字节兜底 | **A 平台替代 + 一次性导入 + 只读导出** |
| Q4 | 加权综合总分 | 无技术障碍，纯产品判断；加权会掩盖「便宜但干不成活」 | **A 仅分维度（MVP）** |
| Q5 | 存量 run 回填 | 技术上不可行：历史 run 无版本归属列，回填只能靠时间推断，归因不成立 | **A 不回填，声明起点** |
| Q6 | 纪律性标注权 | 标注落 `discipline_deductions`，权限按既有 workspace 角色体系 | **A 规则自动 + 授权标注 + Owner 终审** |
| Q7 | AI 介入边界 | C（自动改写切换）触碰 Owner 修法权，架构侧明确不实现 | **B 仅到建议草案（P2）** |
| Q8 | prompt 正文是否同步 Langfuse | 版本 ID + sha256 已足够关联，正文出平台无技术收益 | **A 不同步** |

**架构侧新增的一条请 Owner 确认**：

| # | 决策点 | 选项 | 推荐 |
| --- | --- | --- | --- |
| Q9 | 与 RUYI-177（CLIProxyAPI 接 Langfuse）的关系 | A 共用同一 Langfuse 实例、对齐 trace 命名与 `task_id` 关联键（可拼接模型层与 run 层）；B 各自独立 project 互不关联（实现简单，但看板与模型层数据无法下钻串联） | **A**。Owner 筹备 Langfuse 的目的就是打通观测，分裂成两套等于白建一半 |

## 7. 风险与缓解

- **业务列与版本表不同步**（本 ADR §4.2 的主要代价）→ 同事务写入 + `content_sha256` 一致性自检查询 + 不一致告警。
- **门闸移植不全导致「平台内置门闸弱于 `check_prompts.py`」** → 实现阶段交付「移植清单与逐条对照表」，机械项全移植、判断项显式列为不移植并说明；Owner 可据此判断是否够用。
- **Langfuse 与 RUYI-177 命名口径不一致** → Q9 采纳 A 后，实现阶段第一件事是与 RUYI-177 对齐命名与关联键（该单当前 `running`）。
- **指标被跨任务误读**（token/困惑度受任务异质性干扰）→ 规格 §5.2 的可比性标注与「样本不足不出结论」在看板层强制，非可选提示。
- **P0 顺序倒置**（先做看板后做埋点）→ §5 已把埋点定为强制第一项；埋点未上线前不得开发任何指标计算，否则数据起点作废。

## 8. 验收标准

产品验收条款以规格 §6（V/P/S/A 四级各 4 条 + W1–W6 + D1–D8）为准，本 ADR 不重复。架构侧补充三条可机械判定条款：

- **T1** 任一 prompt 保存事务失败（门闸不通过或库写入失败）后，对应业务列内容与 `prompt_version` 最大版本内容的 `content_sha256` 仍然相等（原子性断言）。
- **T2** 任一新建 run 的 `agent_task_queue.prompt_versions` 中，每个存在的层级键其 `version` 值等于 claim 时刻该 `(scope, scope_id)` 的最大 `version`；该 run 未涉及的层级键缺席（不为 `version: 0`）。
- **T3** Langfuse 导出器未配置或返回错误时，版本保存、切换、回滚与看板查询全部成功返回（外部依赖非阻断断言）。

## 9. 后续

Owner 采纳本 ADR 与 Q1–Q9 后：ADR 落仓 → bd 任务链路拆解（按 §5 的 P0 六项，强制次序）→ 顾小鱼实现，金小欣 QA。
