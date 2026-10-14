# ADR 007：条款资产化与动态注入架构

- 日期：2026年10月12日（初稿）；2026年10月13日（共识评审修订；Owner 决策修订第二版）
- 状态：accepted（Owner 决策 1「批准，按 ADR 拆单建议启动实施」；决策 2「维持原字面目标——阶段 1 即实现动态注入」，本版据此将动态注入纳入第一期并重写方案；决策 3「统计阈值口径维持：方法框架钉死、数值可由实施单细化并经 Owner 确认」）
- 范围：multica 平台 prompt 治理体系——立法提案（RUYI-305）、prompt 版本（RUYI-183）、注入归因与质量度量（RUYI-184）的条款级演进，及 ruyi 提示词仓同步机制的适配。不含本阶段的任何生产代码实现（另拆实施单）。
- 关联：RUYI-737（本 ADR 的需求单）；RUYI-733（Prompt 瘦身治理，其审计结果为存量条款迁移输入）；ADR-001/002（prompt 自进化 phase 1 先例）。

## 背景

平台 prompt 自进化闭环已在载体（carrier）粒度成型：四层载体（workspace/project/squad/agent）的业务列是唯一生效读源，`prompt_version` 做 append-only 版本审计，`agent_task_queue.prompt_versions` 在 claim 时记录层级注入归因，`prompt_quality_daily` 按（载体、版本、日）聚合七维质量，`prompt_proposal` 立法提案池已按 clause 粒度起草（add/revise/remove_clause）、经内容闸五答与 Owner 审批后「合成进载体全文」生效，复盘 runner 自动向提案池供给草案。

这个闭环的度量粒度停在载体版本：条款本身没有稳定 ID、没有独立版本线、作用域变更需要 remove+add 两个提案拼接、条款级注入次数与质量效果无法从载体级数据中分离。RUYI-737 要求把「条款」升级为独立、可版本化、可度量的一等资产，并实现运行时按上下文动态注入（Issue 原字面目标）。

**Owner 决策记录（2026-10-13 决策卡）**：初稿曾推荐「两阶段演进」（条款资产化先行、动态注入留作证据驱动的阶段 2）。Owner 经七视角共识评审与白话解释后明确裁决：**维持 Issue 原字面目标，第一期即实现运行时动态注入**（决策 2），同时批准条款资产化实施（决策 1）、维持统计阈值口径现写法（决策 3）。本版 ADR 据此将方案修订为「资产化 + 动态注入同期实施」的融合形态。

## 术语

- 载体（carrier）：现有四层 prompt 容器（workspace context / project instructions / squad instructions / agent instructions）。
- 条款（clause）：一段可独立立法、修订、启停的治理规则，拥有稳定 `clause_id`；**生效正文的唯一事实源是资产库**。
- 条款版本（clause version）：条款正文的一次生效快照，append-only。
- 绑定（binding）：条款与作用域（workspace/project/squad/agent + scope_id）的生效关系，与正文解耦、独立生命周期；可携带生效条件。
- 稳定上下文：不参与条款生命周期的载体文本（身份、平台交互协议等），存于载体业务列的标记段之外。
- 业务列渲染快照：载体业务列中由资产渲染出的条款段（只读渲染区），加上稳定上下文构成完整业务列文本。
- Effective Clause Set：运行时根据 (workspace, project, squad, agent, task_kind) 上下文计算出的本轮应注入条款集合。
- 注入事件（injection event）：一次 run 实际注入某条款版本的观测记录，是「调用次数」的唯一基础口径。

## As-Is 取证（本 ADR 的事实基础）

1. 四层载体业务列是「当前生效内容」的唯一读源；claim 热路径直读业务列（`server/migrations/908_prompt.up.sql` RUYI-183 段；`server/internal/handler/daemon.go` `buildClaimedTaskResponse`）。
2. `prompt_version` append-only：`(scope, scope_id, version)` 唯一，`content_sha256`、`source`、`scanner_revision`/`gate_result`；所有生效内容变更经 `commitPromptGovernanceVersion` 单漏斗（实体行锁 + 同事务写版本行与业务列，`server/internal/handler/prompt_version.go:631`）。
3. `agent_task_queue.prompt_versions` JSONB 在 claim 组装完成点由 `recordClaimedPromptVersions` 写入，失败 log-and-swallow——「attribution is observability, not a claim precondition」（`daemon.go:3501-3513`）。
4. `prompt_quality_daily` 按 `(scope, scope_id, version, day)` 聚合七维质量，遵守「分子分母分开存，unmeasured ≠ 0」；`injected_tokens` 是版本属性。
5. `prompt_proposal` 提案池：条款粒度提案 + 内容闸五答 + 状态机 `draft → pending_owner → (gate) → enacted/gate_failed/rejected` + `audit_log` append-only；`change_kind` 带 CHECK 约束。现有条款识别是标题行正则（`server/internal/legislation/engine.go:185`）；enacted 事务持载体行锁、可 re-gate re-enact（`server/internal/handler/legislation.go:288,716`）。
6. `prompt_structure_baseline` 双轨（平台表 + ruyi 仓 `tools/check_prompts.py + prompt-structure.json`），平台↔仓经 `snapshot.sh/restore.sh` 同步。
7. 复盘自进化产出 draft 进提案池；jev advisory warn-only 不阻断。claim 存在 reclaim 重投递与重试 attempt，`prompt_quiz_result` 以 `UNIQUE(task_id)` 解决同类幂等。
8. 注入文本是复合物：agent 层经 Mika 合成、leader 任务把 `squad.Instructions` 经 `buildSquadLeaderBriefing` 拼进 `resp.Agent.Instructions`——resp 组装后的文本无法事后从 `prompt_version` 单行还原。

## Gap 分析

- G1 身份缺失：条款只有 `clause_name` 字符串，改名/重名即断链。
- G2 版本缺失：条款修订藏在载体版本 diff 中，V1/V2 无独立可引用版本。
- G3 作用域不可独立管理：跨层迁移 = remove + add 两个提案，非原子。
- G4 计量粒度：注入归因到层，条款级调用次数只能靠关键词反推（AC 明确禁止）。
- G5 质量不可比：质量聚合按载体版本，条款 V1/V2 无法对照。
- G6 双轨基线：平台与仓侧结构基线并存，条款化后同步语义需重定义。
- G7 静态注入：注入内容不随 run 上下文变化——Issue 原字面目标（Owner 决策 2 确认为第一期目标）。

## 方案

### 方案 A：条款独立一等资产 + 运行时动态组装（本 ADR 采纳的核心）

新增 `clause`/`clause_version`/`clause_binding`/`clause_injection` 四表，条款正文事实源移入资产库；claim 热路径按上下文计算 Effective Clause Set 并动态渲染注入；载体业务列退化为「稳定上下文 + 渲染快照」，快照兼作降级兜底。优点：G1–G7 全解；token 可按上下文裁剪。风险：热路径重写、渲染一致性、仓侧工具链适配——由「快照兜底 + 确定性计算 + 全局降级开关」三道缓解（见详细设计与权衡）。

### 方案 B：条款内嵌标记 + 解析层资产化（注入路径不变）

条款文本留在载体里、标记包裹，平台解析建索引资产，注入整层照旧。优点：实施成本最低、仓侧机制全兼容。缺点：G7（动态注入）不解；资产独立性是索引级。**在本 ADR 中作为降级路径的技术形态保留**（见 §5 降级设计），不作为目标形态——Owner 决策 2 已否决「第一期不做动态注入」的路线。

### 采纳结论

采纳方案 A 为第一期目标形态，融合方案 B 的两个构件作为安全网：①业务列渲染快照 = 降级兜底读源；②标记契约 = 渲染段边界与快照解析依据。原「两阶段演进」方案 C 被 Owner 决策 2 否决，其阶段 2 触发条件框架不再使用；其「回退安全网」「计量先于推断」的设计原则保留在融合方案中。

## 详细设计（按 RUYI-737 十问）

### 1. 数据模型与关系

```
clause (id PK, workspace_id, slug, title, status, created_by, created_at,
        UNIQUE(workspace_id, slug))
clause_version (id PK, clause_id, version INT, content TEXT,
                content_sha256, source, carrier_scope, carrier_version INT,
                created_by, created_at,                 -- append-only
                UNIQUE(clause_id, version))
clause_binding (id PK, clause_id, scope, scope_id,
                status(active/superseded/withdrawn),
                conditions JSONB,                       -- 生效条件，NULL=全适用
                since_clause_version, audit_log JSONB, updated_at,
                UNIQUE(clause_id) WHERE status = 'active')
clause_injection (id PK, workspace_id, task_id, clause_id, clause_version INT,
                  carrier_scope, carrier_version INT, created_at,
                  UNIQUE(task_id, clause_id, clause_version))
prompt_proposal (既有表) --→ enacted 时在单事务内写 clause_version/binding
                        --→ 重渲染载体业务列并走既有载体版本化
```

- clause 只存身份与元数据（slug 可改，id 永不变）；正文事实源 = clause_version。
- 唯一约束以数据库部分唯一索引兜底（先例 `idx_prompt_version_scope_version`），跨载体并发由索引拒绝、应用层友好报错；外键完整性照旧应用层事务内校验。
- `clause_injection` 带 `workspace_id` 支撑租户隔离；`UNIQUE(task_id, clause_id, clause_version)` 使 claim 重投递天然幂等（先例 `prompt_quiz_result`）。
- `clause_binding.conditions`：生效条件最小集，形如 `{"task_kinds": ["issue", "chat"]}`，NULL/缺省 = 全适用；取值域 = 平台既有 task kind 枚举。标签、run 长度等更多条件维度明确不做（YAGNI，后续按需扩展 JSONB 结构）。

### 1a. 资产生命周期状态机

- `clause.status`：`active`（生效中）→ `deprecated`（停用：remove_clause/显式停用 enacted 后进入；版本线保留、不进 Effective Clause Set）→ `revoked`（废止：终态不可逆回 active，需新提案重建）。`draft/proposed/approved` 属提案池职责，不是资产状态。
- 与 `clause_binding.status`（active/superseded/withdrawn）职责分账：资产状态记「条款本身启停/废止」，绑定状态记「生效关系变更」；审计者据此区分三种事实。remove_clause 同时落两条状态线。
- 该枚举即 UI 状态徽标词汇表。

### 2. 正文与作用域分别版本化与审批

- 正文变更 = `revise_clause` 提案 → 新 clause_version；作用域/条件变更 = `move_clause` 提案 → 绑定状态线（原子）+ conditions 更新。二者可组合于同一提案，但状态线各自独立可审计。
- 全部转移走 `prompt_proposal.audit_log` append-only。
- move_clause 实施约束：`change_kind` CHECK 约束迁移；跨两载体按确定性顺序加锁防死锁。

### 3. 升级/降级语义

`move_clause` 提案原子变更绑定（agent→project→workspace 为升级，反向为降级），审批通过即单事务完成「旧层移除 + 新层生效 + 两侧业务列重渲染 + 重建结构基线」。clause_id 与版本线不变，历史归因连续。

### 4. 继承、覆盖、冲突、去重

- Effective Clause Set 计算 = 纯函数：①取四层 active 绑定及其条款版本 → ②条件过滤（binding.conditions 匹配当前 task_kind）→ ③同 clause_id 去重（近端层优先，agent > project > squad > workspace）→ ④按 workspace→squad→project→agent 固定序输出渲染。
- 语义冲突是立法期责任：内容闸五答 + 双轨结构基线在提案门闸拦截；运行时不做语义消解，只做确定性排序与去重。
- 条件维度 scope 显式回答：第一期条件仅 task_kind；标签/运行时长等不做（§1）。

### 5. Effective Clause Set 的计算位置、确定性与降级（第一期核心）

- **计算进 claim 热路径**（Owner 决策 2）：`buildClaimedTaskResponse` 内、读业务列的位置替换为「计算有效集合 + 渲染条款段 + 拼接稳定上下文」。
- 确定性：计算是纯 SQL（active 绑定索引点查）+ 固定排序，无模型调用、无随机性；同一 (workspace, project, squad, agent, task_kind) 输入必产出同一集合。第一期不建缓存——绑定变更低频、查询为索引点查，claim 既有十余次查询背景下的增量可忽略（初稿评审已核实解析/查询量级）；缓存留实施单按实测决定。
- **三道失败防线**：
  1. 降级兜底：计算异常/超时 → 注入业务列渲染快照（= 最近一次全量渲染的有效集合，经标记段解析照常打点），写降级告警，绝不阻塞 claim——与 `recordClaimedPromptVersions` log-and-swallow 同一哲学。
  2. 全局降级开关：配置开启后所有 run 走快照注入模式（等价旧静态形态），作为动态链路故障的止损开关与灰度发布手段。
  3. 观测：动态计算延迟、失败率、降级次数进入治理视图（对应 Issue 观测指标）。
- 直编/回退旁路：业务列条款段是只读渲染区——Owner 手工直编稳定上下文（标记段外）合法；直编条款段或 revert/restore 拉回旧文本 → 漂移告警（claim 时比对条款体 sha 与 clause_version），下次重渲染幂等恢复条款段；restore.sh 回写须触发立即重渲染（governed 漏斗内完成）。前提核实项（实施单）：restore 回写必须走 governed 漏斗，直写 DB 路径须先堵漏。

### 6. 调用次数口径（打点位置）

- 打点位置：`buildClaimedTaskResponse` 内、Effective Clause Set 计算完成点——**计算结果即实际注入集合**，逐条款写 `clause_injection`（含 carrier_scope/版本溯源）。
- 降级快照注入时：按快照标记段解析打点；解析失败记降级事件、条款级缺计并告警（缺计不可静默，对账观测兜底）。
- 基础口径（唯一）：调用次数 = clause_version 被实际选中并注入 run 的 Injection Event 数，按 clause_id + version + scope + run 四维追踪。动态注入下「被计算选中」与「被注入」天然一致，无初稿评审担心的归因裂缝。
- 幂等：`UNIQUE(task_id, clause_id, clause_version)` 落 schema；写失败 log-and-swallow + 常设对账观测。
- 「实际触发/行为影响」属未来选项，另立指标命名，禁止与本口径混算。
- 已知边界：打点与既有归因同样存在毫秒级 skew 窗口（现状代码已接受该量级），如实记录。

### 7. 质量度量接入与跨版本比较

- 聚合视图 `clause_quality_daily`（(clause_id, clause_version, day) 唯一）：维度复用 D1/D2/D4/D5/D6/D7，run 集合由 `clause_injection` join 既有 run 结果取得，分子分母分开存，unmeasured ≠ 0。窗口键 = 注入事件时刻的 clause_version；聚合日 = run 终态日（沿用先例）。
- D1 口径：条款 token 按实际注入的条款渲染文本估算（复用 `EstimateTokens`，CJK 感知）；勾稽不变量 = Σ注入条款 token + 稳定上下文 token = 该 run 注入总 token。
- V1/V2 可比性规则（Owner 决策 3 维持）：样本门槛每版本窗 attributable runs ≥ 30 方出百分比结论，低于门槛只出「样本不足」；失败率对比用双比例检验或 Wilson 区间不重叠，α = 0.05；每版本一次计划内对比（对 V(n-1) 完整窗口），禁止逐日重复检验。数值为 ADR 建议值，实施单可凭数据细化、细化值经 Owner 确认（决策 3 口径）。
- 分层/漂移校正维度：agent、attempt、task_kind、同 run 其他层载体版本（`prompt_versions`）、runtime profile。
- 诚实边界：同批多条款修订时条款级 delta 标记 confounded；缺计期间在条款详情页标注。
- 回滚判据：新版本经上述检验显著劣化（α=0.05 且样本达标）→ 自动生成回滚建议 draft 进提案池，仍走 Owner 审批，不自动回滚。
- 效果指标映射：「违规/返工/误阻断」映射 D2/D5/D6/D7；更细行为归因留实施单。

### 8. 立法入口改造与存量迁移

- 提案 enacted 动作改为「单事务内：写 clause/clause_version/clause_binding + 重渲染载体业务列（稳定上下文保留 + 条款段重建）+ 载体版本化 + 重建结构基线」，复用既有载体行锁与 re-gate re-enact 恢复语义。提案池状态机、五答、evidence_anchors、同主题合并沿用。
- 提案引用键改造（G1 闭环关键）：revise/remove/move 提案以 `clause_id` 为引用键，`clause_name` 降为展示字段；提案表迁移新增引用列，存量 name-only 提案迁移或废弃。
- 存量迁移（依赖 RUYI-733 审计清单）：可迁移判据 = 「可独立废止/修订且废止后不影响身份与协议理解的规则」+「正文连续可块化」；身份人设骨架、平台交互协议、状态语义留稳定上下文。分批导入：工具批量生成 draft 提案 → Owner 审批 → 按批 enacted（enacted 即重渲染业务列）；每批可独立回滚（绑定 withdrawn + 重渲染）。禁止一次性无保护切换。
- 兼容期语义：已入库条款有条款级计量与动态注入；未迁移文本留在稳定上下文随载体注入（载体级计量兜底）；治理总览显示迁移覆盖进度。
- 仓侧适配：snapshot 照旧拉业务列全文；restore 回写触发立即重渲染（幂等恢复条款段）；`check_prompts.py`/`prompt-structure.json` 门闸适配条款渲染段识别（实施单内完成，工具变更走既有立法门闸）。

### 9. Token 成本度量与治理视图

条款 token 按实际注入渲染文本估算；治理视图三列：注入次数/覆盖 run 数、token 成本（单条款与有效集合，含勾稽不变量）、质量趋势与版本 delta；另含动态计算延迟/失败率/降级次数（§5）。挂在现有 prompt quality dashboard 下新增条款维度页。

### 10. 权限、安全、审计

- 提案 approve/reject 仅 Owner；agent/member 仅可经复盘或手动起草 draft。治理视图 agent/member 可见性由 UI 实施单定案。
- clause_version append-only + `content_sha256`；绑定、提案审计线 append-only；历史版本不可篡改。
- 条款入库过与 prompt_version 相同的秘密扫描；条款段随载体过 promptscan；动态渲染文本 = 资产内容，无标记噪声进入模型上下文。

## 标记格式（契约草案）

```markdown
<!-- clause:id=cls_a1b2c3d4 rev=3 -->
条款渲染文本……
<!-- /clause -->
```

标记的用途收窄为四项：①业务列中划定条款渲染段边界（重渲染器定位替换）；②降级快照解析打点；③仓侧门闸识别渲染段；④人工阅读定位。`id` 为 `cls_` + 8 位随机小写字母数字、终身不变；`rev` 与 clause_version 一致、门闸校验单调存在。动态注入的正常路径不经过标记解析（直接渲染资产），标记只存在于业务列快照与仓侧文本中。

## 业务列渲染快照与重渲染器（第一期新增核心构件）

- 业务列 = 稳定上下文（标记段外，Owner 可直编）+ 条款渲染段（标记内，只读渲染区）。
- 触发重渲染的事件：enacted / move_clause / 绑定 withdrawn / 资产状态变更 / restore 回写。重渲染 = 从资产取全量有效条款（无条件过滤，全 task_kind 并集）按层序渲染，替换标记段，同事务写业务列并落载体版本。
- 幂等：同状态重渲染产出逐字节一致文本（渲染器确定性要求，列为验收测试对象：同输入两次渲染 diff 为空）。
- 渲染快照的语义：它是「全量有效集合」的固化视图，用作 §5 降级兜底与仓侧同步载体；动态注入的正常路径在 claim 时按 task_kind 过滤渲染，不经快照。

## UI 信息架构需求（设计稿为 UI 实施单前置）

- 条款资产列表页：状态、绑定作用域、条件（task_kind）、当前版本、注入次数、token 成本列。
- 条款详情页：版本时间线、绑定历史（含条件变更）、提案溯源、质量趋势、缺计期间标注。
- 版本对比页：V1/V2 diff 与窗口质量 delta（含 confounded/unmeasured/样本不足标注）。
- 治理总览：有效条款集规模、token 占比、版本分布、迁移覆盖进度、回滚建议列表、动态链路健康度（延迟/失败率/降级次数）。
- 交互需求文档成文时补齐（共识评审 UI 视角缺口）：状态徽标词汇表、空态与降级态清单、agent/member 可见性、回滚建议交互去向、条款量级与筛选假设、载体编辑界面对渲染段与稳定上下文的区分展示（防 Owner 直编误伤渲染段）。
交互需求成文后交黄小云出设计稿并 Review，作为 UI 实施单的前置条件。

## 实施拆单建议（Owner 决策 1 已批准启动）

1. **数据层**：四表迁移（含 conditions、唯一约束、部分唯一索引）+ 标记解析器/重渲染器（含负例用例集与渲染幂等测试）。9xx 迁移编号按项目规范三源核对申请，单 Issue 单编号。
2. **立法链路**：enacted 事务改造（写资产 + 重渲染）+ move_clause（CHECK 迁移 + 双载体加锁）+ 提案引用键 clause_id 化 + 回滚建议 draft 生成。
3. **动态注入链路**：Effective Clause Set 计算（条件过滤/去重/固定序）+ claim 注入组装改造 + 降级兜底 + 全局降级开关 + 打点（幂等）。
4. **计量与聚合**：clause_quality_daily 聚合 + 治理视图 API + 常设对账观测（层集合 ⊆ 关系、解析数 vs 打点数）与 drift 告警。
5. **迁移工具**：733 审计清单 → draft 批量生成 + 分批 enacted + 回滚路径；首批 1–2 个低风险条款试点，试点验收锚点三条：渲染快照与迁移前稳定上下文逐字节一致（条款段除外）、试点周坏标记告警为 0、打点对账一致。
6. **仓侧工具适配**：snapshot/restore/check_prompts 适配渲染段与重渲染触发。
7. **UI**：黄小云设计稿前置，最后实施。
依赖顺序：1→2→3→4 为主线；5 依赖 1–3；6 依赖 2–3；7 最后。QA/观测计划：解析器与 enacted 事务 Feature 测试、渲染幂等、打点幂等（重投递不重复计行）、动态计算降级路径注入快照验证、聚合口径对账（clause_injection vs prompt_versions 层集合 ⊆ 不变式）、治理视图与 prompt_quality_daily 勾稽。

## 用户故事达成映射

- 故事 1（提案生成结构化条款）：达成——enacted 落结构化资产。
- 故事 2（内容与作用域解耦、升降级受控）：达成——move_clause 原子跨层；「缩小适用范围」= 跨层降级 + task_kind 条件收窄（§4）。
- 故事 3（运行时动态注入）：**第一期达成**（Owner 决策 2）——Effective Clause Set 按 (workspace, project, squad, agent, task_kind) 计算，条件维度第一期含 task_kind、不含标签/运行时长。
- 故事 4（精确调用次数）：达成——计算结果即注入集合，clause_injection 精确打点；降级缺计有显式标注。
- 故事 5（条款级质量度量与跨版本比较）：达成——clause_quality_daily + 可检验统计判据（决策 3 口径）；confounded 诚实边界保留。
- 故事 6（全链审计）：达成——提案/版本/绑定/注入/回退/降级全链可溯，三种状态事实分账。

## 权衡与回退

- 核心权衡（Owner 知情决策）：动态注入进第一期把最高风险构件（热路径重写、渲染一致性、仓侧适配）提前，工期与测试面扩大——三道缓解：降级兜底（快照注入）、全局降级开关（灰度与止损）、确定性计算（纯 SQL 固定排序）。
- 渲染快照与资产的事实源分工：资产为唯一事实源，快照为派生视图；漂移由 sha 比对告警 + 幂等重渲染收敛。
- v1 单绑定约束代价：多层同时生效需求只能复制条款（断归因连续性）或扩大作用域（token 扩散）；逃逸 = 后续评估多绑定，治理视图跨层同源标注缓解审计断裂。
- 标记语法契约：只承载快照边界职责（正常注入路径不依赖解析），爆炸半径进一步小于初稿方案；解析门闸在立法期强制校验。
- 回退路径：①单 run 级——计算失败自动降级快照注入；②全局级——降级开关切换为纯快照模式（等价旧静态形态）；③彻底回退——移除条款段标记 + 绑定 withdrawn + 重渲染，资产表退化为历史审计数据，注入与载体版本化行为与现状一致。
- 存量迁移回滚：每批独立回滚（绑定 withdrawn + 重渲染），禁止一次性无保护切换。

## 评审记录（七视角共识评审，2026-10-13 完成；Owner 决策修订见下）

- 产品语义视角（谢小婷）：方案产品语义成立；贡献生命周期状态机（§1a）、用户故事映射、scope 显式回答、兼容期语义、单绑定代价、漏计标注。
- 数据模型与运行时视角（顾小鱼）：方向成立；贡献打点数据源事实（resp 复合文本不可还原，§6 打点改为计算点直采）、schema 并发约束（§1）、直编/restore 漂移检测（§5）、提案引用键 clause_id 化与 move_clause 加锁（§8/§2）、对账不变式修正（层集合 ⊆）、skew 窗口记录。
- 质量归因与失败模式视角（金小欣）：统计判据可检验化（§7，Owner 决策 3 维持其框架）、edit/revert 旁路失败模式（§5）、打点幂等与常设对账（§6/QA 计划）、D1 剥离后口径（本版改为实际注入渲染文本口径，随动态注入自然收敛）、分层维度补齐、QA 计划补四项。
- UI 视角（黄小云）：四类页面需求成立；前置缺口 6 项沉淀 UI 节成文清单。
- **Owner 决策修订（2026-10-13）**：决策 2 否决「两阶段演进」（初稿方案 C），裁决第一期即实现动态注入——本版据此重写方案节、§5/§6/渲染快照节与拆单建议；初稿方案 C 的「回退安全网、计量口径、统计框架」设计原则保留。被否决方案：两阶段演进（触发条件制动态注入）、纯方案 B（标记资产化不做动态注入）。
- 结论：七视角评审完成、重大分歧全部闭环、Owner 三决策落定；进入实施拆单。
