# ADR 007：条款资产化与注入归因架构

- 日期：2026年10月12日（初稿）；2026年10月13日（共识评审修订定稿）
- 状态：proposed（七视角共识评审完成，待 Owner 批复；批准后置 accepted）
- 范围：multica 平台 prompt 治理体系——立法提案（RUYI-305）、prompt 版本（RUYI-183）、注入归因与质量度量（RUYI-184）的条款级演进，及 ruyi 提示词仓同步机制的适配。不含本阶段的任何生产代码实现（另拆实施单）。
- 关联：RUYI-737（本 ADR 的需求单）；RUYI-733（Prompt 瘦身治理，其审计结果为存量条款迁移输入）；ADR-001/002（prompt 自进化 phase 1 先例）。

## 背景

平台 prompt 自进化闭环已在载体（carrier）粒度成型：四层载体（workspace/project/squad/agent）的业务列是唯一生效读源，`prompt_version` 做 append-only 版本审计，`agent_task_queue.prompt_versions` 在 claim 时记录层级注入归因，`prompt_quality_daily` 按（载体、版本、日）聚合七维质量，`prompt_proposal` 立法提案池已按 clause 粒度起草（add/revise/remove_clause）、经内容闸五答与 Owner 审批后「合成进载体全文」生效，复盘 runner 自动向提案池供给草案。

这个闭环的度量粒度停在载体版本：一条提案修订一个条款，产生的却是整个载体文本的新版本。条款本身没有稳定 ID、没有独立版本线、作用域变更需要 remove+add 两个提案拼接、条款级注入次数与质量效果无法从载体级数据中分离。RUYI-737 要求把「条款」升级为独立、可版本化、可度量的一等资产，并评估运行时动态注入。

## 术语

- 载体（carrier）：现有四层 prompt 容器（workspace context / project instructions / squad instructions / agent instructions），业务列仍为注入热路径的唯一读源。
- 条款（clause）：一段可独立立法、修订、启停的治理规则，拥有稳定 `clause_id`。
- 条款版本（clause version）：条款正文的一次生效快照，append-only。
- 绑定（binding）：条款与作用域（workspace/project/squad/agent + scope_id）的生效关系，与正文解耦、独立生命周期。
- 注入事件（injection event）：一次 run 的 claim 组装实际包含某条款版本的观测记录，是「调用次数」的唯一基础口径。
- 稳定上下文：不参与条款生命周期的载体文本（身份、平台交互协议等），继续随载体整体版本化。

## As-Is 取证（本 ADR 的事实基础）

1. 四层载体业务列（`workspace.context`、`project.instructions`、`squad.instructions`、`agent.instructions`）是「当前生效内容」的唯一读源；claim 热路径直读业务列，注释明确钉死不 join 版本表（`server/migrations/908_prompt.up.sql` RUYI-183 段；`server/internal/handler/daemon.go` `buildClaimedTaskResponse`）。
2. `prompt_version` 表 append-only：`(scope, scope_id, version)` 唯一，`content_sha256`、`source(import/edit/revert/auto_snapshot)`、`scanner_revision`/`gate_result`；切换/回滚 = 追加新行，版本线即审计线。所有生效内容变更经 `commitPromptGovernanceVersion` 单漏斗（实体行锁 + 同事务写版本行与业务列，「业务列 = 最新版本内容」是平台保证的强不变式，`server/internal/handler/prompt_version.go:631`）。
3. `agent_task_queue.prompt_versions` JSONB 在 claim 组装完成点由 `recordClaimedPromptVersions` 写入，仅记录实际注入的层级版本，未注入的层缺席而非 0；失败 log-and-swallow——「attribution is observability, not a claim precondition」（`daemon.go:3501-3513`）。
4. `prompt_quality_daily` 按 `(scope, scope_id, version, day)` 聚合 D1 token 成本、D2 discipline（含覆盖度）、D4 工具失败率、D5 重试、D6 失败根因（环境类排除）、D7 首过率；D3 perplexity 按 runtime profile 单独评分；全表遵守「分子分母分开存，unmeasured ≠ 0」；`injected_tokens` 是版本属性（按版本内容静态估算）。
5. `prompt_proposal` 提案池：`carrier_scope + target_section + change_kind(add/revise/remove_clause) + clause_name + clause_text`，内容闸五答结构化字段，`evidence_anchors`，状态机 `draft → pending_owner → (gate) → enacted/gate_failed/rejected`，`enacted_version`、`rollback_reason`、`merged_from` 同主题合并、`source(manual/retrospective)`、`audit_log` append-only。`change_kind` 带 CHECK 约束，扩类型需迁移。
6. `prompt_structure_baseline` 每载体登记已批准章节集/顺序与条款名注册表，每次 enacted 后从合成全文重建；现有条款识别是标题行正则（`server/internal/legislation/engine.go:185`）；enacted 事务（`runGateAndEnactTx`）持目标载体行锁，mid-flight 崩溃遗留的 approved 行可 re-gate re-enact（`server/internal/handler/legislation.go:288,716`）。ruyi 仓侧另有 `tools/check_prompts.py + prompt-structure.json` 双轨结构基线，平台↔仓经 `snapshot.sh/restore.sh` 同步。
7. 复盘自进化（`server/internal/retrospective`）产出 draft 进提案池并做同主题合并；jev advisory（RUYI-347）提供 warn-only 软判决，不阻断门闸。claim 存在 reclaim 重投递与重试 attempt（`055_task_lease_and_retry`），同 task_id 可能多次到达组装点；`prompt_quiz_result` 以 `UNIQUE(task_id)` 解决同类幂等（`908_prompt.up.sql:545,568`）。
8. 注入文本是复合物：agent 层经 Mika 合成（`ComposeMikaInstructions`）、leader 任务把 `squad.Instructions` 经 `buildSquadLeaderBriefing` 拼进 `resp.Agent.Instructions`——resp 组装后的文本无法事后从 `prompt_version` 单行还原，条款打点的文本采集必须发生在各归因点（业务列原文处）。

## Gap 分析

- G1 身份缺失：条款只有 `clause_name` 字符串，注册在结构基线里；改名/重名即断链，无法作为外键引用。
- G2 版本缺失：条款修订藏在载体版本 diff 中，条款 V1/V2 无独立可引用版本。
- G3 作用域不可独立管理：跨层迁移 = remove + add 两个提案，非原子、语义不连贯。
- G4 计量粒度：注入归因到层，条款级调用次数只能靠关键词反推（AC 明确禁止的口径）。
- G5 质量不可比：质量聚合按载体版本，一个版本 delta 是多条条款修订的混合效果，条款 V1/V2 无法对照。
- G6 双轨基线：平台 `prompt_structure_baseline` 与仓侧 `prompt-structure.json` 并存，条款资产化后同步语义需要重定义。

## 方案比较

### 方案 A：条款独立一等资产 + 运行时动态组装

新增 `clause`/`clause_version`/`clause_binding`/`clause_injection` 四表，claim 热路径按上下文计算 Effective Clause Set（继承、优先级、冲突、去重），运行时渲染条款段并按 run 上下文裁剪；载体业务列退化为渲染产物。

- 优点：G1–G5 全解；token 可按 run 裁剪，为成本优化打开空间。
- 缺点与风险：热路径重写，破坏「业务列唯一读源」的既定安全设计；Effective Clause Set 的确定性、缓存、失败降级成为新的核心复杂度；渲染产物与 snapshot/restore 仓侧同步、双轨结构基线直接冲突（快照拉回的是渲染文本，回写即破坏单一事实源）；存量条款全量入库，迁移面最大；条款库故障需要新的降级路径。一次性跨越资产化与动态注入两道门槛，违反手术式演进。

### 方案 B：条款内嵌标记 + 解析层资产化（注入读源与注入语义不变）

条款文本继续存放在载体业务列中，以机器可读标记包裹（HTML 注释形态）。平台在 enacted/编辑保存时解析标记，维护 `clause`/`clause_version`/`clause_binding` 索引表（正文以载体为源、`content_sha256` 指回载体版本，附载体版本映射）；claim 时照旧整层注入，但在既有归因点并列采集各层注入文本，按条款逐条写 `clause_injection`；注入组装末端新增确定性剥离变换，模型看到干净文本。质量度量由 `clause_injection` join 现有 run 结果聚合，不新建采集链路。跨层迁移新增 `move_clause` 提案类型（单次审批原子变更绑定）。

- 优点：注入读源与注入语义不变（业务列仍是唯一读源，安全网不变）；仓侧 snapshot/restore、双轨结构基线全兼容（标记就在文本里，门闸继续校验合成全文）；可逐条款渐进迁移，无一次性切换；实施成本低。
- 缺点：标记语法成为长期契约，需要解析器与门闸校验（成对、id 存在、版本递增）；「条款正文不能脱离载体存在」，资产独立性是索引级而非存储级；整层注入，token 不能按 run 裁剪；新增两处确定性变换（文本采集、标记剥离），属热路径的机械增量而非零改动。

### 方案 C（推荐）：两阶段演进——以 B 起步，动态注入留作证据驱动的阶段 2

- 阶段 1（本 ADR 范围）：按方案 B 完成条款资产化——稳定 ID、版本线、绑定解耦、条款级注入计量、条款级质量视图、立法入口改造、存量迁移工具。注入读源与注入语义不变。
- 阶段 2（触发条件制，届时另立 ADR）：仅当出现明确证据——治理视图显示有效条款集 token 占比显著过高且存在明显不适用于多数 run 的条款类目，或出现按任务类型条件化条款的确定需求——且继承/冲突规则经至少一个迁移批次实际数据验证、`clause_injection` 数据积累足够（≥8 周）后，评估把 Effective Clause Set 计算移入 claim 热路径。方案 A 是阶段 2 的候选形态，不默认启动；触发条件不满足则阶段 1 形态即长期形态，不构成技术债（索引与存储同源）。

推荐理由：G1–G5 的根因是「条款没有身份、版本、绑定与计量」，不是「注入不够动态」。先把最难回头补的地基（稳定 ID、append-only 版本线、与正文解耦的绑定、精确打点口径）建好，动态注入作为可选升级留给证据决策；同时注入读源不变使全程保留「回退到整层载体文本」这一根本安全网。符合简单优先与手术式演进。

## 阶段 1 详细设计（按 RUYI-737 十问）

### 1. 数据模型与关系

```
clause (id PK, workspace_id, slug, title, status, created_by, created_at,
        UNIQUE(workspace_id, slug))
clause_version (id PK, clause_id, version INT, content TEXT,
                content_sha256, source, carrier_scope, carrier_version INT,
                created_by, created_at,                 -- append-only
                UNIQUE(clause_id, version))
clause_binding (id PK, clause_id, scope, scope_id,
                status(active/superseded/withdrawn), since_clause_version,
                audit_log JSONB, updated_at,
                -- v1 同一 clause 至多一个 active 绑定（部分唯一索引兜底）
                UNIQUE(clause_id) WHERE status = 'active')
clause_injection (id PK, workspace_id, task_id, clause_id, clause_version INT,
                  carrier_scope, carrier_version INT, created_at,
                  UNIQUE(task_id, clause_id, clause_version))
prompt_proposal (既有表) --→ enacted 时在单事务内写 clause_version/binding
                        --→ 合成带标记的载体全文并走既有载体版本化
```

- clause 只存身份与元数据（slug 可改，id 永不变）；正文只存于 clause_version；存储源仍是载体业务列，clause_version.content 是生效快照副本（一致由 `content_sha256` 校验）。
- 唯一约束以数据库部分唯一索引兜底（house rule 允许唯一索引，先例 `idx_prompt_version_scope_version`），跨载体并发（如 revise 与 move_clause 并行）由索引拒绝、应用层给友好报错；外键完整性照旧应用层事务内校验。
- `clause_injection` 带 `workspace_id` 支撑租户隔离查询与按空间清理；`UNIQUE(task_id, clause_id, clause_version)` 使 claim 重投递/重试 attempt 的重复写入天然幂等（冲突不插入，先例 `prompt_quiz_result`）。

### 1a. 资产生命周期状态机（Issue 委托 ADR 定义）

- `clause.status` 值域与转移：`active`（生效中，提案 add/revise enacted 后进入或保持）→ `deprecated`（停用：remove_clause 或显式停用提案 enacted 后进入；正文与版本线保留、不再渲染进载体）→ `revoked`（废止：撤销类提案 enacted 后进入，终态不可逆回 active，需新提案重建）。`draft/proposed/approved` 属提案池职责，不是资产状态。
- 与 `clause_binding.status` 的职责分界：资产状态（active/deprecated/revoked）记录条款本身的启停/废止；绑定状态（active/superseded/withdrawn）记录生效关系的变更（move_clause 使旧绑定 superseded）。审计者据此区分「条款废止」「条款停用」「绑定撤回」三种事实；remove_clause 同时落资产 deprecated 与绑定 withdrawn 两条状态线。
- `clause.status` 枚举值即 UI 状态徽标词汇表（黄小云前置缺口项）。

### 2. 正文与作用域分别版本化与审批

- 正文变更 = `revise_clause` 提案 → 新 clause_version；作用域变更 = `move_clause` 提案 → 绑定状态线（旧绑定 superseded/withdrawn + 新绑定 active，单事务原子）。二者可在同一提案中组合，但版本与绑定状态线各自独立记录、各自可审计。
- 全部转移继续走 `prompt_proposal.audit_log` append-only，无需新增 event log。
- move_clause 实施约束：`change_kind` CHECK 约束需迁移扩展；单事务跨两个载体，按作用域确定性顺序（如 scope 枚举序）加锁两行防死锁。

### 3. 升级/降级语义

`move_clause` 提案原子变更绑定（agent→project→workspace 为升级，反向为降级），审批通过即单事务完成「旧层移除 + 新层生效」，并按既有规则重建两侧结构基线。升降级不改变 clause_id 与版本线，历史归因天然连续。

### 4. 继承、覆盖、冲突、去重

- 运行时只做确定性操作：同一 clause_id 全局渲染一次；多层出现同 id 视为迁移残留，解析门闸直接拦截（立法期责任，不带进运行时）。
- 语义冲突是立法期责任：内容闸五答（conflict/dedup）+ 双轨结构基线在提案门闸拦截；运行时不做语义消解，与现状一致（各层文本本就按固定顺序叠加，近端语义覆盖远端由模型理解）。
- 展示顺序固定：workspace → squad → project → agent（与现有注入顺序同构），不做运行时重排。
- **scope 条件维度显式回答**：阶段 1 的 scope 仅四层（workspace/project/squad/agent + scope_id），不含 run、任务类型、标签等条件维度；「缩小适用范围」在阶段 1 只有跨层降级语义，不支持层内条件缩小。条件维度与层内条件化同为阶段 2 评估项，由其触发条件统一约束。

### 5. Effective Clause Set 的计算位置（阶段 1 口径）

阶段 1 不进热路径——claim 照旧读业务列整层注入，「计算」只是既有归因点的解析步骤（读取的是已随载体版本固化的文本，天然确定）。失败语义分级：

- 解析失败（标记损坏）：该条款不计量、文本照常注入，写条款级治理告警——与 jev advisory「报告不阻断」、`recordClaimedPromptVersions` log-and-swallow 同一哲学，绝不阻塞 claim。
- **非提案直编/回退旁路（edit/revert/snapshot/restore）失败模式**（共识评审定稿新增）：
  - 解析器按 `content_sha256` 幂等：同内容不重复建 clause_version。
  - revert/restore 拉回旧文本时：条款体 sha 与当前 clause_version 一致 → 绑定重指旧版本并告警；sha 不一致且标记 rev 变小 → 告警并按「该条款不计量」降级，等待下次提案收敛（资产侧只告警不自动改写）。
  - stale binding 探测：claim 打点时顺带比对条款体 sha256 与 `clause_version.content_sha256`，失配写 drift 告警（不阻断）；active 绑定的条款缺席当前载体版本 → 告警并按明确规则转 Owner 处置（自动 withdrawn 留实施单定案）。
  - 前提核实项（列入实施单）：restore.sh 回写须走 `commitPromptGovernanceVersion` governed 漏斗——若存在直写 DB 路径则「有标记必有版本行」不成立，须先堵漏。
- 阶段 2 若引入真实动态组装，另行 ADR 定义缓存指纹与降级路径。

### 6. 调用次数口径（打点位置与实现）

- 打点位置：`buildClaimedTaskResponse` 内写入 `prompt_versions` 的同一点；数据源为**各归因点并列累积的注入文本** `promptTexts[scope]`（业务列原文，含 Mika 合成前的 agent 行与 briefing 拼接点，见 As-Is 第 8 条——resp 复合文本无法事后还原，故不读 prompt_version）；条款版本号直接取标记 `rev`（门闸已保证 rev 与 clause_version 一致），sha 从注入文本现算。
- 基础口径（唯一）：调用次数 = clause_version 被实际选中并注入 run 的 Injection Event 数，可按 clause_id + version + carrier_scope + run 四维追踪。
- 幂等：`UNIQUE(task_id, clause_id, clause_version)` 落 schema，reclaim 重投递冲突不插入。写失败沿用 log-and-swallow，但计量缺失不可静默——对账为常设观测（见 QA 计划）。
- 「实际触发/行为影响」（如成员回报引用条款名、Review 结论归因）属未来选项，另立指标命名，禁止与本口径混算。
- 已知边界：打点与既有归因同样存在毫秒级 skew 窗口（业务列读取与版本号读取非同一快照，现状代码已接受该量级），如实记录不另加锁。

### 7. 质量度量接入与跨版本比较

- 聚合视图 `clause_quality_daily`（(clause_id, clause_version, day) 唯一）：维度复用 D1/D2/D4/D5/D6/D7 口径，run 集合由 `clause_injection` join 既有 run 结果表取得，分子分母分开存，unmeasured ≠ 0。**窗口键 = 注入事件（claim）时刻的 clause_version；聚合日 = run 终态日**（沿用 `prompt_quality_daily` 先例），版本切换边界的归属无歧义。
- D1 口径钉死：条款 token 按**剥离后模型可见文本**估算（复用 `EstimateTokens`，CJK 感知）；勾稽不变量 = Σ条款 token + 非条款文本 token = 载体 `injected_tokens`（同一剥离后口径），治理视图与 `prompt_quality_daily` 总量勾稽以此为准。
- V1/V2 可比性规则（统计判据可检验化，共识评审定稿新增）：
  - 样本门槛：每版本窗 attributable runs ≥ 30 方可出百分比结论，低于门槛只出「样本不足」。
  - 检验方法：失败率对比用双比例检验或 Wilson 区间不重叠判定，α = 0.05；分布漂移按分层占比对比标注。
  - 检验频次：每版本只做一次计划内对比（对 V(n-1) 完整窗口），禁止逐日重复检验（多次偷看同一窗口批量制造伪告警）。
  - 上述数值为 ADR 建议值，实施单可在不改变方法框架的前提下细化，细化值经 Owner 确认（待 Owner 决策定口径，见决策卡）。
  - 分层/漂移校正维度：agent、attempt、任务类型（实施单落地时定义数据来源字段）、**同 run 其他层载体版本（取自 `prompt_versions`）、runtime profile**。
  - 诚实边界：同一载体版本内多条款同批修订时，条款级 delta 标记 confounded，质量归因以「单条款修订批次」为最强；坏标记「不计量」期间的注入次数静默偏低，条款详情页标注「存在解析失败未计量期间」。
- 回滚判据：新版本窗口 attributable 失败率经上述检验显著劣化（α=0.05 且样本达标）→ 自动生成回滚建议 draft 进提案池，仍走 Owner 审批，不自动回滚。
- 效果指标映射：Issue 观测指标「违规/返工/误阻断」映射 D2（discipline 扣分）/D5（重试）/D6（归因失败）/D7（首过率）；更细的行为归因留实施单定义。

### 8. 立法入口改造与存量迁移

- 提案 enacted 动作从「合成纯文本」改为「单事务内：写 clause/clause_version/clause_binding + 合成带标记载体全文 + 载体版本化 + 重建结构基线」，复用既有载体行锁与 re-gate re-enact 恢复语义。提案池状态机、五答、evidence_anchors、同主题合并全部沿用。
- **提案引用键改造（G1 闭环的关键，共识评审定稿新增）**：revise/remove/move 提案以 `clause_id` 为引用键，`clause_name` 降级为展示字段——沿用 name 匹配会令 G1 在资产化后复发。提案表需迁移：新增 clause_id 引用列，存量 name-only 提案迁移或废弃。
- 存量迁移（依赖 RUYI-733 审计清单）：可迁移判据 = 「可独立废止/修订且废止后不影响身份与协议理解的规则」+「正文连续可块化」（标记包裹要求连续文本块，散落多处的规则先经一次文本归集提案）；身份人设骨架、平台交互协议、状态语义等必背核心留稳定上下文。分批导入：工具按清单批量生成 draft 提案 → Owner 审批 → 按批 enacted；每批可独立回滚（移除标记 + 载体版本回退 + 绑定 withdrawn）。禁止一次性无保护切换。
- 兼容期语义：已入库条款有条款级计量与治理视图；未迁移条款仅载体级计量兜底；治理总览显示迁移覆盖进度（已入库 X / 存量 Y），防止把条款视图误当全量。
- 仓侧适配：标记属文本一部分，snapshot/restore 与 check_prompts.py 门闸机制不变；`prompt-structure.json` 基线重建逻辑适配条款标记节（实施单内完成，工具变更走既有立法门闸）；restore 回写旧版本时资产侧只告警不自动改写（见 §5）。

### 9. Token 成本度量与治理视图

条款 token 估算复用 `EstimateTokens`；治理视图三列：条款注入次数/覆盖 run 数、条款 token 成本（单条款与有效集合，含 §7 勾稽不变量）、质量趋势与版本 delta。挂在现有 prompt quality dashboard 下新增条款维度页（信息架构需求见下节），后端聚合由 `clause_injection`/`clause_quality_daily` 支撑。

### 10. 权限、安全、审计

- 审批权限沿用现状：提案 approve/reject 仅 Owner；agent/member 仅可经复盘或手动起草 draft。条款治理视图对 agent/member 只读可见与否由 UI 实施单定案（黄小云前置缺口）。
- clause_version append-only + `content_sha256`；绑定与提案审计线 append-only；历史版本不可篡改（不提供改写路径）。
- 条款入库前过与 prompt_version 相同的秘密扫描（scanner_revision/gate_result 复用）；标记文本随载体过 promptscan；注入剥离后的标记不进模型上下文。

## 标记格式（契约草案）

```markdown
<!-- clause:id=cls_a1b2c3d4 rev=3 -->
条款正文……
<!-- /clause -->
```

- `id`：`cls_` + 8 位随机小写字母数字，条款终身不变；`slug`/标题不入标记（降低噪声）。
- `rev`：与 clause_version.version 一致，门闸校验单调与存在性。
- 注入组装末端剥离标记行（确定性变换，非注入语义变更）；剥离仅影响模型可见文本，存储与审计保留原文。**剥离函数列为验收测试对象**：对无标记文本输入输出逐字节一致（这是回退安全网「与现状逐字节一致」成立的前提），并在载体编辑界面让标记行可见可辨识，降低 Owner 手工编辑误伤概率。

## UI 信息架构需求（阶段 1 仅出需求，设计稿为对应实施单前置）

- 条款资产列表页：状态、绑定作用域、当前版本、注入次数、token 成本列。
- 条款详情页：版本时间线、绑定历史、提案溯源（evidence_anchors 链接）、质量趋势图、解析失败未计量期间标注。
- 版本对比页：clause V1/V2 diff 与对应窗口质量 delta（含 confounded/unmeasured/样本不足标注）。
- 治理总览：有效条款集规模、token 占比、版本分布、迁移覆盖进度、回滚建议列表。
- 交互需求文档成文时补齐（共识评审 UI 视角前置缺口，不阻塞本 ADR）：状态徽标词汇表（§1a 枚举）、样本不足/坏标记/回退条款的空态与降级态展示清单、agent/member 可见性、回滚建议交互去向（跳转提案池或内嵌审批）、条款量级与筛选维度假设（作用域 × 状态 × 有无 active 绑定）。
交互需求成文后交黄小云出设计稿并 Review，作为 UI 实施单的前置条件。

## 实施拆单建议（Owner 批准本 ADR 后执行，本单不开发）

1. **数据层**：clause/clause_version/clause_binding/clause_injection 迁移（含唯一约束与部分唯一索引）+ 解析器（含门闸校验规则与负例用例集）。
2. **立法链路**：proposal enacted 事务改造（写资产 + 合成带标记全文）+ move_clause 类型（CHECK 迁移 + 双载体确定性加锁）+ **提案引用键改造（clause_id 化）** + 回滚建议 draft 生成。
3. **计量与聚合**：归因点文本采集 + claim 解析打点（幂等唯一约束）+ clause_quality_daily 聚合（窗口键/聚合日口径）+ 治理视图 API + 常设对账观测与 drift 告警。
4. **迁移工具**：733 审计清单 → draft 提案批量生成 + 分批 enacted + 回滚路径（首批发 1–2 个低风险条款试点）。
5. **UI**：依赖黄小云设计稿，最后实施。
依赖顺序 1→2→3→4→5；4 的试点批次是阶段 1 的验收锚点，锚点三条：剥离后模型可见文本与迁移前逐字节一致；试点周坏标记告警为 0；试点条款打点行数与 claim 解析数对账一致。

QA/观测计划：

- 解析器与 enacted 事务 Feature 测试；打点幂等性（重投递不重复计行）。
- 解析器负例用例集作为验收清单：标记未闭合、重复 id、rev 不存在/跳号、正文含 `<!-- /clause -->` 字面量、标记嵌套。
- 剥离函数逐字节一致性测试（无标记输入输出不变）。
- 常设对账观测：每 run「clause_injection 出现的层集合 ⊆ prompt_versions 记录的层集合」（正确不变式，非行数相等）；解析条款数 vs 打点行数不符即告警——写失败 best-effort 但缺失不静默。
- 坏标记条款级告警出口：治理视图告警列表 + 运行日志；回退条款历史质量行在治理视图标注「已回退、不参与活跃口径」。
- 治理视图与 prompt_quality_daily 总量勾稽（按 §7 勾稽不变量）。

## 用户故事达成映射（共识评审定稿新增，随 ADR 提请 Owner 确认）

- 故事 1（提案生成结构化条款）：阶段 1 达成——提案 enacted 落结构化资产。达成形态是「单点存放 + 结构化索引」，条款文本仍物理存于载体（与字面「不复制进 Prompt 文件」有差距，见下方修订声明）。
- 故事 2（内容与作用域解耦、升降级受控）：阶段 1 达成——move_clause 原子跨层；「缩小适用范围」限跨层降级语义（见 §4 scope 显式回答）。
- 故事 3（运行时动态注入）：**修订为阶段 2 目标**（触发条件制）——见修订声明。
- 故事 4（精确调用次数）：阶段 1 达成——clause_injection 精确打点；坏标记期间有显式标注兜底。
- 故事 5（条款级质量度量与跨版本比较）：阶段 1 达成——clause_quality_daily + 可检验统计判据；多条款同批修订存在 confounded 诚实边界。
- 故事 6（全链审计）：阶段 1 达成——提案/版本/绑定/注入/回退全链可溯，三种状态事实（废止/停用/撤回）分账。

**对 Issue 产品目标的显式修订声明**：Issue 背景愿景「Prompt 只消费运行时计算得到的有效条款集合」与故事 3，在本 ADR 中被修订为「条件触发的阶段 2 目标」；阶段 1 达成形态是「条款单点立法与结构化索引、注入仍整层载体文本」。此修订经 Owner 在本 ADR 审批中确认后方生效，确认即构成对原字面目标的修订认可。

## 权衡与回退

- 核心权衡：接受「条款资产是索引级独立」的 v1 形态，换取注入读源零风险与仓侧机制全兼容；动态注入的收益（token 裁剪、条件化）让渡给阶段 2 的证据决策。
- v1 单绑定约束的已知代价（共识评审定稿新增）：同一条款需「多层同时生效」时只能复制为两条条款（断开归因连续性）或扩大作用域（token 成本扩散到全空间）；逃逸路径 = 阶段 2 评估多绑定，或以治理视图的跨层同源条款标注缓解审计断裂。
- 标记语法是新增长期契约：解析门闸在立法期强制校验，坏标记的爆炸半径被限制在「该条款不计量 + 显式告警」，不影响注入；直编/回退旁路有 §5 三条规则兜底。
- 回退路径（全程保留）：移除标记即回到纯载体文本——资产表退化为历史审计数据，注入与载体版本化行为与现状逐字节一致；不需要迁移回滚脚本以外的任何代码回退。
- 阶段 2 预置否决权：触发条件不满足或证据不足时，阶段 1 形态即为长期形态，不构成技术债（索引与存储同源）。

## 评审记录（七视角共识评审，2026-10-13 完成）

- 产品语义视角（谢小婷）：方案 C 产品语义成立，无重大分歧；故事 3 移阶段 2 不伤及其余故事达成（先计量后动态是正确依赖序）。贡献：生命周期状态机定义（§1a）、用户故事达成映射与目标修订声明（见前节）、scope 条件维度显式回答（§4）、兼容期语义（§8）、漏计窗口标注与效果指标映射（§7）、单绑定代价（权衡节）。
- 数据模型与运行时视角（顾小鱼）：方向成立，4 项设计缺口全部吸收——打点数据源改为归因点并列采集文本（§6，resp 复合文本不可还原）、schema 并发约束与幂等落库（§1）、直编/restore 漂移检测（§5）、提案引用键 clause_id 化与 move_clause 加锁（§8/§2）；对账不变式修正为层集合包含关系（QA 计划）；skew 窗口如实记录（§6）。
- 质量归因与失败模式视角（金小欣）：2 项重大分歧均已解决——统计判据可检验化（§7：门槛/检验方法/频次/数值口径待 Owner 决策）、edit/revert 旁路失败模式（§5 三条规则 + drift 告警）；3 项钉死全部吸收——打点幂等与常设对账（§6/QA 计划）、热路径表述精确化与 D1 剥离后口径及勾稽不变量（方案 B/§7）、分层维度补齐（§7）；QA 计划补 4 项全收（负例用例集、试点锚点三条、day 语义、告警出口与回退标注）。
- UI 视角（黄小云）：无重大分歧，四类页面需求成立；设计启动前置缺口 6 项沉淀进 UI 信息架构需求节的成文清单。
- 未核验声明（金小欣）：jev advisory warn-only 引用自初稿 As-Is 调研，非其视角承重点，未独立核验；该项在初稿与定稿中均为背景性引用，不承重。
- 被否决/未采纳方案：无推翻级分歧；无被整体否决的备选子方案（方案 A 整体保留为阶段 2 候选）。
- 结论：七视角评审完成，全部重大分歧已在修订中闭环，无未解决分歧；进入 Owner 审批。
