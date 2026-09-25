## ADR-002：Prompt 自进化闭环 —— 评估体系论证、数据源可选化与增量四块的架构承载

状态：待 Owner 采纳（v2，取代 ADR-001 的 §4.4 与 §5，其余章节继续有效）
作者：蔡小星（架构）
日期：2026-09-25
基线：worktree SHA `965ce1a64dd108155b18bb8cbc281856a3b5b410`（ADR-001 引用的 `31977c5af38aa7cc3a39195a7f1159016c1cb86b` 已不是当前 tip，本文所有代码事实按当前 tip 复核）
产品输入：`RUYI-179-prompt-spec-v1.md`（§1–§8）、`RUYI-179-prompt-spec-addendum-v1.1.md`（§9–§17）
上游需求：Owner 评论 `01a0d42a-dc4b-7190-a41d-aa9bcc119e28`（09-25 00:06）七条扩展

---

### 1. 本文回答什么

ADR-001 只解决了「版本管理 + 三维度看板」。Owner 七条把本单扩成「Prompt 自进化闭环」，其中第 2–6 条的产品口径由 PM 在 v1.1 规格中定完，剩下两条是架构问题，本文回答它们，并为 PM 新增的四块功能给出架构承载：

- **第 1 条**：评估体系不能只有 Owner 随口提的三个维度，要论证维度集合为什么是这些、为什么够用、哪些能测哪些测不了。→ §2
- **第 7 条**：Langfuse 降为可选数据源，没有 Langfuse 时用平台自己的 run 数据和各 runtime 的会话 jsonl。→ §3
- 测验机制、每日扫描提案池、知识显化、skill 提案与版本化四块的架构落点与新增实体清点。→ §4–§7
- 分期重估与开放问题统一表。→ §8–§10

---

### 2. 评估体系的完备性论证（Owner 第 1 条）

#### 2.1 论证方法：先定义「一次 run 能观测到什么」，再从中筛维度

不从「想到几个维度」出发，而从「平台对一次 agent run 到底留下了哪些可机械读取的痕迹」出发，穷举观测面，再判断每个观测面能否归因到 Prompt。观测面共五类，来自当前库表与代码：

| 观测面 | 载体（当前已存在） | 能否归因到 Prompt |
| --- | --- | --- |
| 输入成本 | `task_usage.input_tokens` / `cache_read_tokens` / `cache_write_tokens` / `context_tokens`（迁移 032/908/915/213） | 能（注入内容直接决定） |
| 产出行为 | `task_message`（迁移 026：seq/type/tool/content/input/output） | 能（Prompt 规定行为纪律） |
| 终局结果 | `agent_task_queue.status` / `failure_reason`（迁移 055）+ `pkg/taskfailure` 的 27 个 Reason 枚举 | 部分（须剔除环境类原因） |
| 重试与回合 | `agent_task_queue.attempt` / `max_attempts` / `parent_task_id`（迁移 055）；`task_usage.turns` / `compactions`（迁移 908/915） | 部分（同上） |
| 人工裁决 | issue 状态流转、评论、`activity_log`（迁移 001） | 弱（噪声大，本期不入指标） |

从这五类观测面推导出的维度集合如下，Owner 提的三个维度落在其中，另外四个是本文补入的。

#### 2.2 七个维度及其论证

**D1 注入成本（Token 量）** —— Owner 原提。取 `prompt_version.content` 的 token 估算与 `task_usage.input_tokens` 的关系。**为什么留**：这是 Prompt 唯一 100% 可归因的指标，Prompt 涨一个字，每一次 run 都付钱。**局限**：越短越便宜但不等于越好，必须与 D2–D5 联读，单独看会激励把 Prompt 删空。

**D2 纪律性** —— Owner 原提。定义为「Prompt 中可机械判定的禁令与必做项在本次 run 中的违反次数」。**为什么可算**：本项目 Prompt 里的纪律条款绝大多数是机械可判的（「禁止纯文本 mention」「评论必须用 `--content-file`」「同一轮必须三选一」），可从 `task_message` 的 tool/input 与最终评论正文中检出。**局限**：只覆盖能写成规则的那部分条款，覆盖率本身要作为看板上的元指标显示，不能让读者误以为「零扣分 = 完全守纪」。

**D3 困惑度** —— Owner 原提。**架构结论：本期不做「模型困惑度」，改为可观测代理指标。** 真困惑度需要 logprobs，我们既不持有模型侧 logprobs，CLIProxyAPI 链路也未暴露；硬做就是假数据。代理指标取三项 `task_message` 可算量：同一工具同一参数的重复调用次数、同一文件的重复读取次数、达到迭代上限（`ReasonIterationLimit`）的比例。这三项共同刻画「agent 在原地打转」，是 Owner 想通过困惑度看到的东西。**这是一次口径替换，请 Owner 在 Q17 明确认可或否决。**

**D4 轮次失败率** —— Owner 第 1 条新提。定义为一次 run 内 `is_error = true` 的工具结果占全部工具结果的比例。**当前有采集缺口**：各 runtime 在 `pkg/agent/*.go`（如 `claude.go:705`）都解析了 `is_error`，但 `daemon.go:8863/8960/9004` 落库时只写 `Seq/Type/Tool/Content/Input/Output`，`task_message` 表没有 `is_error` 列，这个信号在写库时被丢弃。**处置建议**：加一列 `task_message.is_error BOOLEAN`（可空，NULL = 该 runtime 或该消息类型测不了，沿用 `task_usage` 已确立的「NULL ≠ 0」语义），daemon 写路径与 `client.go:491` 的 `TaskMessageData` 同步加字段。这是本单唯一必需的新增采集点，约 0.5 人日，且历史数据无法回填——**上线前的 run 在 D4 上永远是空值，看板必须显示「自 YYYY-MM-DD 起有数据」。**

**D5 重试次数** —— Owner 第 1 条新提。**零新增采集**：`agent_task_queue.attempt` / `max_attempts` / `parent_task_id` 已在库（迁移 055）。口径为「同一逻辑任务的 attempt 分布」。

**D6 错误根因分布** —— Owner 第 1 条新提。**零新增采集，且不需要新建分类体系**：`pkg/taskfailure/failure.go` 已有 27 个 Reason 常量，含 `agent_error.*` 细分（如 `ReasonAgentEmptyOrUnparseableOutput`、`ReasonAgentContextOverflow`、`ReasonAgentBlocked`），`classify.go` 的 `Classify()` / `NormalizeDaemonReason()` 已在写路径统一归一。**关键架构约束：这 27 个 Reason 必须先分账再进指标。** `ReasonRuntimeOffline`、`ReasonRuntimeReconnectTimeout`、`ReasonRuntimeRecovery`、`ReasonEnvironmentPrepareFailed`、`ReasonAgentProviderQuotaLimit`、`ReasonAgentProviderCapacityOrRateLimit`、`ReasonAgentProviderNetwork`、`ReasonAgentProviderServerError`、`ReasonAgentRuntimeMissingExecutable` 这一组是环境与供应商故障，与 Prompt 质量无关；把它们算进「Prompt 变差」会让每一次机房抖动都表现为 Prompt 回归。看板上必须固定分成「可归因 Prompt」与「环境/供应商」两栏，**分母只用前者**。

**D7 一次过率** —— 本文补入。定义为「交付未经返工即通过 Review/QA 的 run 占比」。**为什么必须有**：D1–D6 全是过程指标，一个 Prompt 可以做到零违纪、零重试、低 token，同时交付一堆没用的东西。没有结果指标，整套评估会系统性地奖励「安静地做错事」。**取数**：本项目的 Review/QA 结论以 issue 状态流转与评论为载体，机械提取有噪声，因此 D7 在本期**只做 issue 级粗口径**（是否发生过 `in_review` → `in_progress` 回退），并明确标注为低置信度指标。

#### 2.3 完备性结论与边界

**完备性的含义**：D1–D7 覆盖了 §2.1 五类观测面中全部四类可归因面，第五类（人工裁决）因噪声过大只取 D7 一个粗口径。在不新增模型侧数据采集（logprobs、token 级概率）的前提下，**这是当前平台数据能支撑的全部**。

**明确不做的**：语义质量评分（需要裁判模型，成本与可信度都不过关，且 v1.1 §9 的测验机制已在用「机器判定 + 金标准」替代它）；跨 workspace 横向对比（权限模型不同）；单条纪律条款级别的因果归因（样本量不足）。

**指标可信度分级必须上看板**：高（D1/D5/D6）、中（D2/D4）、低（D3 代理口径/D7）。禁止把不同置信度的维度合成单一「质量分」——合成分会把低置信度维度的噪声传染给整块看板，也让「变好还是变差」这个 Owner 最关心的判断失去可追溯性。

---

### 3. 数据源可选化（Owner 第 7 条）

#### 3.1 结论：三源分层，Langfuse 是可选增强而非依赖

代码事实：仓库内当前**零 Langfuse / OTLP 引用**；RUYI-177 正在做的是 CLIProxyAPI 接 Langfuse，属模型调用层，与本单的 run 业务层是两套数据。因此把 Langfuse 放在关键路径上是拿一个尚不存在的外部依赖做本单的前置条件。

三源分层：

- **源 A：平台 run 数据（必选，唯一真相源）** —— `agent_task_queue`、`task_message`、`task_usage`、`pkg/taskfailure`。D1、D2、D4、D5、D6、D7 全部由源 A 算出。**没有任何一个看板数字依赖源 B 或源 C。**
- **源 B：各 runtime 会话 jsonl（可选，证据引用用）** —— Owner 第 7 条点名。定位不是指标源，而是**提案与知识条目的证据落点**：提案要求 ≥1 条证据引用（v1.1 §10.2），jsonl 提供比 `task_message` 更完整的现场。**架构约束**：jsonl 在 runtime 本机，服务端不持有；因此证据引用**只存定位元数据**（runtime id / task id / 文件相对路径 / 行号区间 / 时间戳），不落原文。确需摘录时必须过脱敏管道（复用 `promptscan`），且摘录长度设硬上限。
- **源 C：Langfuse（可选增强）** —— 单向导出，可整体关闭；看板不从 Langfuse 回读（沿用 ADR-001 §4.6）。Langfuse 未接入时，自进化功能**全功能可用**，只是少一个外部分析界面。

#### 3.2 接口形态

不为三源建抽象层。源 A 是 SQL 查询，源 B 是 daemon 侧文件读取，源 C 是导出器 —— 三者的数据形态、生命周期、可用性保证完全不同，硬造一个 `DataSource` 接口只会得到一个所有方法都在返回「不支持」的接口。按 CLAUDE.md「不做没被要求的灵活性」，各自直连，由调用方按可用性分支。

#### 3.3 降级语义必须显式

任一可选源不可用时，**不得静默降级**。看板与提案详情页显示「本条无 jsonl 证据（runtime 离线）」「Langfuse 导出已关闭」，而不是让读者以为数据完整。这是 ADR-001 §8 验收断言 T3 的延伸。

---

### 4. 测验机制的架构承载（Owner 第 2 条 / v1.1 §9）

**执行载体**：复用现有 agent task 通道，不新建执行器。一次测验 = 按题组向目标 agent 派发一批 task，判分由服务端机械规则执行。

**隔离（v1.1 §16 硬要求）**：测验 run 不得接触生产 Issue 与真实用户数据。当前 `agent_task_queue.issue_id` 自迁移 033 起可空（chat 任务即无 issue），**测验 task 走 `issue_id IS NULL` 路径**，并新增来源标记列区分于 chat。数据库隔离本期**不做**（成本远超收益，且判分只读题面自带的验收条款），改为约束题面：题面不得引用生产实体 ID。**这是一处有意的降级，请 Owner 在 Q18 确认接受。**

**触发时机**：v1.1 §14 的 C1 把测验定为「版本生效后必跑」。架构上这是唯一可行的时机——ADR-001 §4.2 定稿「保存即生效、无草稿态」，生效前没有可测对象。因此测验的产出是**事后标签**，不是准入闸门，这与 v1.1 §15 Q10 的推荐一致。

**新增实体**：题库 `evolve_quiz_item`、测验批次 `evolve_quiz_run`、单题成绩 `evolve_quiz_result`。成绩按 v1.1 §14 的 C4 挂四级版本归因键（复用 ADR-001 §4.3 的 `prompt_versions JSONB` 同构键）。

---

### 5. 每日扫描与提案池的架构承载（Owner 第 3 条 / v1.1 §10）

**调度载体：完全复用 `internal/scheduler`，不新造定时机制。** `spec.go` 的 DB-backed `JobSpec` 体系已提供分布式锁（`sys_cron_executions` 的 `(job_name, scope_kind, scope_id, plan_time)` 唯一键，迁移 113）、幂等、审计与 catch-up。注册点 `cmd/server/main.go:710-727`，与 `rollup_task_usage_hourly`、`autopilot_schedule_dispatch`、`plugin_hook_schedule_dispatch` 并列。

**CatchUpMode 取 `CatchUpEveryPlan`**：每个 D-1 日窗口有独立业务意义，服务中断两天后恢复必须把两天分别扫出来，不能只扫最近一天。

**扫描口径的盲区（v1.1 §10.1 未覆盖，需定）**：规格写「扫描每个 Issue、每个 run」，但 `agent_task_queue.issue_id` 可空——chat 会话产生的 run 没有 Issue。本文建议：**本期只扫有 issue 的 run**，chat run 不入扫描范围（chat 缺少验收结论，提案质量无法保证）。列为 Q19。

**提案池实体**：`evolve_proposal`（统一承载五类提案）+ `evolve_proposal_evidence`（证据引用，对应 §3.1 源 B 的元数据结构）。状态机按 v1.1 §10.4：`草稿 → 已采纳 → 已生效/已转化 → 归档`，旁路「需修订」。

**「打勾修订」链路的架构要点**：勾选采纳 → 生成新版本**草稿内容 + diff**（不是版本行）→ 跑立法门闸（ADR-001 §4.5 的 `promptscan.Scan` + `check_prompts.py` 机械子集）→ FAIL 转「需修订」不产生版本 → Owner 一步确认 → 同事务写 `prompt_version` 行 + 更新业务列 + 回写提案状态。**注意这条链路与 ADR-001 §4.2「保存即生效、无草稿态」并不冲突**：草稿态在提案池里，不在版本表里，版本表依然只有已生效的行。

---

### 6. 知识显化的架构承载（Owner 第 4 条 / v1.1 §11）

**检索能力的硬约束，必须写进规格与界面预期**：本平台**没有 tsvector，也没有 pgvector**。全文检索基建是 **pg_bigm**（迁移 032 建扩展，036 重建索引），且 RDS 上的 pg_bigm 1.2 **不支持 ILIKE 索引扫描**——迁移 036 的注释原文即为此。因此：

- 知识库检索是 **bigram 子串匹配**，不是语义检索，也没有相关度排序与词干还原。查「踩坑」能命中「踩坑记录」，查「部署失败」命中不了「发布未成功」。
- 所有检索谓词必须写成 `LOWER(col) LIKE LOWER(pattern)`，索引建成 `gin (LOWER(col) gin_bigm_ops)`，且按 CLAUDE.md 用 `CREATE INDEX CONCURRENTLY` 单文件单语句。
- 迁移 032/036 都用 `DO $$ ... EXCEPTION` 包住以兼容无 pg_bigm 的 CI；按 CLAUDE.md 第三条 DB 规则，**本单的索引迁移必须沿用同一容错写法，且后续迁移用 `IF EXISTS`**——条件跳过的迁移仍会记入 `schema_migrations`。
- 无 pg_bigm 的环境检索退化为全表 `LIKE` 扫描，**数据量上去会慢**。本期可接受（知识条目量级是千级），但要在 ADR 里留明。

**与 bd memories 的关系**：PM v1.1 §11.4 结论为「替代 + 一次性汇入」。架构侧确认此结论可行且推荐——bd 是文件型本地库，与平台 DB 双写必然漂移，同步器是负资产。汇入是一次性脚本，不留常驻通道。**bd 的任务跟踪职能不变，仅知识库职能迁出。**

**新增实体**：`evolve_knowledge`（三类合一，`kind` 判别列，三态生命周期）。

---

### 7. Skill 提案与版本化的架构承载（Owner 第 5、6 条 / v1.1 §12）

**核查结论：PM §12.4「版本管理实体扩一类，不新造机制」在实现上不是零成本。** 代码事实（迁移 008）：

```sql
CREATE TABLE skill (id, workspace_id, name, description, content, config JSONB,
                    created_by, created_at, updated_at, UNIQUE(workspace_id, name));
```

`skill` 表**没有任何版本字段**，迁移 161 只加了 `agent_skill.enabled`，迁移 368 只加了 `skill.plugin_installation_id`。同时**没有任何 skill 使用记录载体**——`agent_skill` 只记录「挂没挂」，不记录「用没用、用了效果如何」。

因此第 6 条落地需要两件新东西，不是一件：

1. **`skill_version` 表** —— 结构可与 `prompt_version` 同构（`version INT` / `content` / `content_sha256` / `source` / `change_note` / `author_*`，`UNIQUE(skill_id, version)`）。ADR-001 §4.1 的单表设计**不扩 `scope` 枚举吞并 skill**：skill 的作用域是 workspace 级且有独立的 `skill_file` 子表，塞进 `prompt_version` 会让 `scope_id` 的语义分裂。两张同构表比一张多态表清晰。
2. **skill 使用记录** —— 评价机制（采纳率、使用组 vs 未使用组对比）**必须先有「这次 run 用了哪些 skill」的事实**。当前无处可查。建议沿用 ADR-001 §4.3 的做法：在 task claim 时把生效 skill 的 `(skill_id, version)` 一并写进 `agent_task_queue` 的归因 JSONB，零额外查询。**与 D1–D7 埋点同批上线，否则评价机制上线即无数据，且历史不可回填。**

`skill` 表带 FK（早于 no-FK house rule 的老表）；新表按迁移 399 的现代范式建，**不加 FK**，关系由应用层维护。

**样本纪律**：v1.1 §12.3 要求两组各 ≥5 才出方向性标签。架构侧确认：样本不足时接口返回「样本不足」而非返回一个数字，前端不得自行兜底渲染 0。

---

### 8. 新增实体清点与过度设计自查

本文引入的新表（不含 ADR-001 已有的 `prompt_version`、`prompt_quality_daily`）：

| 表 | 用途 | 归属 Owner 条款 |
| --- | --- | --- |
| `evolve_quiz_item` | 题库 | 第 2 条 |
| `evolve_quiz_run` | 测验批次 | 第 2 条 |
| `evolve_quiz_result` | 单题成绩 | 第 2 条 |
| `evolve_proposal` | 五类提案统一池 | 第 3、5 条 |
| `evolve_proposal_evidence` | 证据引用 | 第 3 条 |
| `evolve_knowledge` | 认知/教训/踩坑三类合一 | 第 4 条 |
| `skill_version` | skill 版本 | 第 6 条 |

加列：`task_message.is_error`（D4，唯一必需新增采集）、`agent_task_queue` 归因 JSONB 扩 skill 键。

**过度设计自查（我的角色条款红线：新增 >2 个新实体即须提醒先简化）**：7 张新表远超阈值，**我在此显式提醒并给出简化方案**。可裁掉的：`evolve_quiz_run` 可并入 `evolve_quiz_result`（批次号作列而非表），`evolve_proposal_evidence` 可作 `evolve_proposal` 的 JSONB 列（证据条数是个位数，不需要独立查询）。裁掉后为 5 张。**但根因不在设计冗余，在范围**——Owner 七条本身就是四个独立功能（测验、提案池、知识库、skill 版本化）加一个评估体系，任何设计都无法把它们压进 2 张表。真正的简化手段是**分期**，见 §9。

---

### 9. 分期重估

**ADR-001 §5 的「P0 六项约 11 人日」作废**，本文不给新的总数字——范围仍在 Owner 决策中（Q2 的分期结论未定），此时给总人日是拿一个会变的数当承诺。给的是**强制次序与不可并行的依赖**：

**第一阶段（数据地基，必须最先，且必须整批上线）**
1. `prompt_version` 表与写路径（ADR-001 §4.1/§4.2）
2. run 版本归属埋点（ADR-001 §4.3）+ skill 归因键（§7）
3. `task_message.is_error` 列与 daemon 写路径（§2.2 D4）
4. 存量四级 Prompt 导入首版

**为什么必须整批**：2、3 的数据**不可回填**。埋点晚一周，看板与 skill 评价就永远缺一周的历史。**在第一阶段上线前不得开始任何指标计算、看板或评价功能的开发**——这是 ADR-001 §4.3 已定的纪律，此处扩展到 skill 与 `is_error`。

**第二阶段（看得见）**：D1–D7 的 rollup + 自进化看板 + 版本历史与切换界面。

**第三阶段（闭环）**：每日扫描 job + 提案池 + 打勾修订链路。这是 Owner 第 3 条的核心价值，PM 在 v1.1 §15.1 也建议其优先级高于知识库与 skill。

**第四阶段（并行可选）**：测验机制 / 知识库 / skill 版本化三选，彼此无依赖，可按 Owner 优先级排序，也可拆成独立 Issue。

**独立且不阻塞**：Langfuse 导出器（§3 源 C），随时可做可不做。

---

### 10. 开放问题统一表（Q1–Q19）

Q1–Q8 为 PM v1 提出，Q9 为 ADR-001 新增，Q10–Q16 为 PM v1.1 新增，Q17–Q19 为本文新增。架构侧意见与 PM 推荐一致处不再重复论证。

| 编号 | 问题 | 推荐 | 架构侧说明 |
| --- | --- | --- | --- |
| Q1 | 版本切换的权限范围 | 按 PM v1 推荐 | 架构无影响 |
| Q2 | 分期与优先级 | 见 §9 四阶段 | **本文重排**：数据地基 → 看板 → 提案闭环 → 三选一；PM v1.1 §15.1 的「闭环与测验优先于知识库与 skill」已吸收 |
| Q3 | 版本保留策略 | 按 PM v1 推荐 | 架构无影响 |
| Q4 | 存量导入的作者归属 | 按 PM v1 推荐 | 架构无影响 |
| Q5 | 版本比较的展示形式 | 按 PM v1 推荐 | 架构无影响 |
| Q6 | 修订确认权归属 | **建议与 Q16 合并作答** | 两者同构，分开定会出现「提案能确认、知识不能确认」的错配 |
| Q7 | 看板的时间粒度 | 按 PM v1 推荐 | 日聚合，与 `prompt_quality_daily` 一致 |
| Q8 | Prompt 正文的外泄边界 | 按 PM v1 推荐 | **口径延续到新对象**：知识条目、提案草稿、skill 正文、题面同样不出平台 |
| Q9 | 是否与 RUYI-177 共用 Langfuse 实例 | A：共用 | RUYI-177 是模型调用层，本单是 run 业务层，共用实例但数据集分开 |
| Q10 | 测验的约束力 | A：标签制不做闸门 | 架构侧强确认：无草稿态 ⇒ 测验必在生效之后 ⇒ 物理上当不了闸门 |
| Q11 | 打勾后是否保留 diff 确认步 | A：保留 | 门闸在确认前跑，FAIL 不产生版本（§5） |
| Q12 | 扫描窗口 | A：每日扫 D-1 | `CatchUpEveryPlan`（§5） |
| Q13 | 知识库与 bd 的关系 | A：替代 + 一次性汇入 | 架构确认可行，双写必漂移（§6） |
| Q14 | 知识是否注入 agent 上下文 | A：纯检索不注入 | 注入会直接推高 D1 注入成本并污染评估基线 |
| Q15 | skill 够格判据阈值 | A：用默认值 | 架构无影响 |
| Q16 | 知识条目的确认权 | B：授权角色 + Owner 终审 | 建议与 Q6 合并 |
| **Q17** | **困惑度口径替换** | **A：接受代理指标** | 真困惑度需 logprobs，平台不持有；A = 用「重复调用/重复读取/触顶比例」三项代理，B = 本期不做该维度。硬做真困惑度 = 假数据 |
| **Q18** | **测验隔离降级** | **A：接受约束题面，不做数据库隔离** | A = 测验走无 issue 路径 + 题面禁引生产实体 ID；B = 建独立测验数据库（成本数倍） |
| **Q19** | **chat run 是否入扫描** | **A：本期不扫 chat run** | chat 无验收结论，提案质量无法保证；B = 一并扫，接受噪声 |

---

### 11. 架构验收断言

沿用 ADR-001 §8 的 T1–T3，本文补 T4–T7：

- **T1**（承接）版本行与业务列写入原子：同事务失败时两者均不变。
- **T2**（承接）run 版本归属：任一已完成 run 可查出其四级 Prompt 版本号，层级缺席表示为键不存在而非 `version: 0`。
- **T3**（承接）外部依赖非阻断：Langfuse 关闭或不可达时，版本管理与看板全功能可用。
- **T4** 指标分账：`failure_reason` 属环境/供应商类的 run 不进入 D6 的 Prompt 归因分母，测试需覆盖至少一个 `ReasonRuntimeOffline` 样本。
- **T5** 缺数据可见：`task_message.is_error` 为 NULL 的历史 run 在 D4 上显示为「无数据」，不显示为 0%。
- **T6** 门闸 fail-closed：提案采纳后门闸 FAIL 时，`prompt_version` 表无新行产生。
- **T7** 样本不足不编数：skill 对比两组任一 <5 时，接口返回「样本不足」标记，前端不渲染数值。

---

### 12. 后果与回退

**接受的后果**：新增 5–7 张表与一个每日 job，底座复杂度实质上升；知识检索能力受 pg_bigm 限制，语义检索要等 pgvector 才可能；D3、D7 是低置信度维度，可能引发误读，靠看板的可信度分级缓解；测验隔离是约束式而非物理式。

**回退路径**：四个阶段彼此松耦合，任一阶段可独立停在原地不影响已上线部分。最坏情况下停在第一阶段（版本管理可用、埋点已在采集），后续能力随时续做而不丢历史数据。**唯一不可回退的是埋点时机**——没埋的那段历史永远补不回来，这也是把它列为第一阶段唯一强制项的原因。
