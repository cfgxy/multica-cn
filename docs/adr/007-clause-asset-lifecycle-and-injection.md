# ADR 007：条款资产化与注入归因架构

- 日期：2026年10月12日
- 状态：proposed（待架构共识评审与 Owner 批复；批准后置 accepted）
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
2. `prompt_version` 表 append-only：`(scope, scope_id, version)` 唯一，`content_sha256`、`source(import/edit/revert/auto_snapshot)`、`scanner_revision`/`gate_result`；切换/回滚 = 追加新行，版本线即审计线。
3. `agent_task_queue.prompt_versions` JSONB 在 claim 组装完成点写入，仅记录实际注入的层级版本，未注入的层缺席而非 0（`daemon.go` `promptVersionScopeIDs`）。
4. `prompt_quality_daily` 按 `(scope, scope_id, version, day)` 聚合 D1 token 成本、D2 discipline（含覆盖度）、D4 工具失败率、D5 重试、D6 失败根因（环境类排除）、D7 首过率；D3 perplexity 按 runtime profile 单独评分；全表遵守「分子分母分开存，unmeasured ≠ 0」。
5. `prompt_proposal` 提案池：`carrier_scope + target_section + change_kind(add/revise/remove_clause) + clause_name + clause_text`，内容闸五答结构化字段，`evidence_anchors`，状态机 `draft → pending_owner → (gate) → enacted/gate_failed/rejected`，`enacted_version`、`rollback_reason`、`merged_from` 同主题合并、`source(manual/retrospective)`、`audit_log` append-only。
6. `prompt_structure_baseline` 每载体登记已批准章节集/顺序与条款名注册表，每次 enacted 后从合成全文重建；ruyi 仓侧另有 `tools/check_prompts.py + prompt-structure.json` 双轨结构基线，平台↔仓经 `snapshot.sh/restore.sh` 同步。
7. 复盘自进化（`server/internal/retrospective`）产出 draft 进提案池并做同主题合并；jev advisory（RUYI-347）提供 warn-only 软判决，不阻断门闸。

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

### 方案 B：条款内嵌标记 + 解析层资产化（注入路径不变）

条款文本继续存放在载体业务列中，以机器可读标记包裹（HTML 注释形态）。平台在 enacted/编辑保存时解析标记，维护 `clause`/`clause_version`/`clause_binding` 索引表（正文以载体为源、`content_sha256` 指回载体版本，附载体版本映射）；claim 时照旧整层注入，但在写入 `prompt_versions` 的同一点解析各层实际注入文本，按条款逐条写 `clause_injection`；注入前剥离标记行，模型看到干净文本。质量度量由 `clause_injection` join 现有 run 结果聚合，不新建采集链路。跨层迁移新增 `move_clause` 提案类型（单次审批原子变更绑定）。

- 优点：热路径零改动（业务列仍是唯一读源，安全网不变）；仓侧 snapshot/restore、双轨结构基线全兼容（标记就在文本里，门闸继续校验合成全文）；可逐条款渐进迁移，无一次性切换；实施成本低。
- 缺点：标记语法成为长期契约，需要解析器与门闸校验（成对、id 存在、版本递增）；「条款正文不能脱离载体存在」，资产独立性是索引级而非存储级；整层注入，token 不能按 run 裁剪。

### 方案 C（推荐）：两阶段演进——以 B 起步，动态注入留作证据驱动的阶段 2

- 阶段 1（本 ADR 范围）：按方案 B 完成条款资产化——稳定 ID、版本线、绑定解耦、条款级注入计量、条款级质量视图、立法入口改造、存量迁移工具。注入路径不动。
- 阶段 2（触发条件制，届时另立 ADR）：仅当出现明确证据——治理视图显示有效条款集 token 占比显著过高且存在明显不适用于多数 run 的条款类目，或出现按任务类型条件化条款的确定需求——且继承/冲突规则经至少一个迁移批次实际数据验证、`clause_injection` 数据积累足够（≥8 周）后，评估把 Effective Clause Set 计算移入 claim 热路径。方案 A 是阶段 2 的候选形态，不默认启动。

推荐理由：G1–G5 的根因是「条款没有身份、版本、绑定与计量」，不是「注入不够动态」。先把最难回头补的地基（稳定 ID、append-only 版本线、与正文解耦的绑定、精确打点口径）建好，动态注入作为可选升级留给证据决策；同时热路径零改动使全程保留「回退到整层载体文本」这一根本安全网。符合简单优先与手术式演进。

## 阶段 1 详细设计（按 RUYI-737 十问）

### 1. 数据模型与关系

```
clause (id PK, workspace_id, slug, title, status, created_by, created_at)
clause_version (id PK, clause_id FK→clause, version INT, content TEXT,
                content_sha256, source, carrier_scope, carrier_version INT,
                created_by, created_at)          -- append-only
clause_binding (id PK, clause_id FK→clause, scope, scope_id,
                status(active/superseded/withdrawn), since_clause_version,
                audit_log JSONB, updated_at)     -- 独立生命周期
clause_injection (id PK, task_id, clause_id, clause_version INT,
                  carrier_scope, carrier_version INT, created_at)
prompt_proposal (既有表) --→ enacted 时在单事务内写 clause_version/binding
                                          --→ 合成带标记的载体全文并走既有载体版本化
```

- clause 只存身份与元数据（slug 可改，id 永不变）；正文只存于 clause_version；存储源仍是载体业务列，clause_version.content 是生效快照副本（一致由 `content_sha256` 校验）。
- v1 约束：同一 clause 同时至多一个 active 绑定（跨层需求走 move_clause）；多绑定作为阶段 2 评估项。
- 沿用 house rule：不建外键约束，应用层事务内校验。

### 2. 正文与作用域分别版本化与审批

- 正文变更 = `revise_clause` 提案 → 新 clause_version；作用域变更 = `move_clause` 提案 → 绑定状态线（旧绑定 superseded/withdrawn + 新绑定 active，单事务原子）。二者可在同一提案中组合，但版本与绑定状态线各自独立记录、各自可审计。
- 全部转移继续走 `prompt_proposal.audit_log` append-only，无需新增 event log。

### 3. 升级/降级语义

`move_clause` 提案原子变更绑定（agent→project→workspace 为升级，反向为降级），审批通过即单事务完成「旧层移除 + 新层生效」，并按既有规则重建两侧结构基线。升降级不改变 clause_id 与版本线，历史归因天然连续。

### 4. 继承、覆盖、冲突、去重

- 运行时只做确定性操作：同一 clause_id 全局渲染一次；多层出现同 id 视为迁移残留，解析门闸直接拦截（立法期责任，不带进运行时）。
- 语义冲突是立法期责任：内容闸五答（conflict/dedup）+ 双轨结构基线在提案门闸拦截；运行时不做语义消解，与现状一致（各层文本本就按固定顺序叠加，近端语义覆盖远端由模型理解）。
- 展示顺序固定：workspace → squad → project → agent（与现有注入顺序同构），不做运行时重排。

### 5. Effective Clause Set 的计算位置（阶段 1 口径）

阶段 1 不进热路径——claim 照旧读业务列整层注入，「计算」只是 claim 组装点的解析步骤（读取的是已随载体版本固化的文本，天然确定）。失败语义：解析失败（标记损坏）按条款级告警降级为「该条款不计量、文本照常注入」，绝不阻塞 claim——与 jev advisory「报告不阻断」同一哲学。阶段 2 若引入真实动态组装，另行 ADR 定义缓存指纹与降级路径。

### 6. 调用次数口径（打点位置）

- 打点位置：`buildClaimedTaskResponse` 内写入 `prompt_versions` 的同一点；解析各层实际注入文本（版本 content 已可从 prompt_version 取得），对每个命中的条款写一行 `clause_injection`。
- 基础口径（唯一）：调用次数 = clause_version 被实际选中并注入 run 的 Injection Event 数，可按 clause_id + version + carrier_scope + run 四维追踪。
- 「实际触发/行为影响」（如成员回报引用条款、Review 结论归因）属未来选项，另立指标命名，禁止与本口径混算。

### 7. 质量度量接入与跨版本比较

- 聚合视图 `clause_quality_daily`（(clause_id, clause_version, day) 唯一）：维度复用 D1/D2/D4/D5/D6/D7 口径，run 集合由 `clause_injection` join 既有 run 结果表取得，分子分母分开存，unmeasured ≠ 0。
- V1/V2 可比性规则：以版本生效日切窗对照；按（agent、任务类型、attempt）分层校正任务分布；最小样本门槛（attributable runs 达标才出百分比结论）；版本重叠期 run 分桶不混算。诚实边界：同一载体版本内多条款同批修订时，条款级 delta 标记 confounded，质量归因以「单条款修订批次」为最强。
- 伪提升防护：分布漂移（分层占比变化超阈值）显式标注；回滚判据 = 新版本窗口 attributable 失败率相对上一版本基线显著劣化且样本达标 → 自动生成回滚建议 draft 进提案池，仍走 Owner 审批，不自动回滚。

### 8. 立法入口改造与存量迁移

- 提案 enacted 动作从「合成纯文本」改为「单事务内：写 clause/clause_version/clause_binding + 合成带标记载体全文 + 载体版本化 + 重建结构基线」。提案池状态机、五答、evidence_anchors、同主题合并全部沿用。
- 存量迁移（依赖 RUYI-733 审计清单）：可迁移判据 = 「可独立废止/修订且废止后不影响身份与协议理解的规则」；身份人设骨架、平台交互协议、状态语义等必背核心留稳定上下文。分批导入：工具按清单批量生成 draft 提案 → Owner 审批 → 按批 enacted；每批可独立回滚（移除标记 + 载体版本回退 + 绑定 withdrawn）。禁止一次性无保护切换。
- 仓侧适配：标记属文本一部分，snapshot/restore 与 check_prompts.py 门闸机制不变；`prompt-structure.json` 基线重建逻辑适配条款标记节（实施单内完成，工具变更走既有立法门闸）。

### 9. Token 成本度量与治理视图

条款 token 估算复用 `EstimateTokens`（CJK 感知）；治理视图三列：条款注入次数/覆盖 run 数、条款 token 成本（单条款与有效集合）、质量趋势与版本 delta。挂在现有 prompt quality dashboard 下新增条款维度页（信息架构需求见下节），后端聚合由 `clause_injection`/`clause_quality_daily` 支撑。

### 10. 权限、安全、审计

- 审批权限沿用现状：提案 approve/reject 仅 Owner；agent/member 仅可经复盘或手动起草 draft。
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
- 注入时剥离标记行；剥离仅影响模型可见文本，存储与审计保留原文。

## UI 信息架构需求（阶段 1 仅出需求，设计稿为对应实施单前置）

- 条款资产列表页：状态、绑定作用域、当前版本、注入次数、token 成本列。
- 条款详情页：版本时间线、绑定历史、提案溯源（evidence_anchors 链接）、质量趋势图。
- 版本对比页：clause V1/V2 diff 与对应窗口质量 delta（含 confounded/unmeasured 标注）。
- 治理总览：有效条款集规模、token 占比、版本分布、回滚建议列表。
交互需求成文后交黄小云出设计稿并 Review，作为 UI 实施单的前置条件。

## 实施拆单建议（Owner 批准本 ADR 后执行，本单不开发）

1. **数据层**：clause/clause_version/clause_binding/clause_injection 迁移 + 解析器（含门闸校验规则）。
2. **立法链路**：proposal enacted 事务改造（写资产 + 合成带标记全文）+ move_clause 类型 + 回滚建议 draft 生成。
3. **计量与聚合**：claim 解析打点 + clause_quality_daily 聚合 + 治理视图 API。
4. **迁移工具**：733 审计清单 → draft 提案批量生成 + 分批 enacted + 回滚路径（首批发 1–2 个低风险条款试点）。
5. **UI**：依赖黄小云设计稿，最后实施。
依赖顺序 1→2→3→4→5；4 的试点批次是阶段 1 的验收锚点。QA/观测计划：解析器与 enacted 事务 Feature 测试、打点幂等性、聚合口径对账（clause_injection 行数 vs prompt_versions 注入层数）、治理视图与 prompt_quality_daily 总量勾稽。

## 权衡与回退

- 核心权衡：接受「条款资产是索引级独立」的 v1 形态，换取热路径零风险与仓侧机制全兼容；动态注入的收益（token 裁剪、条件化）让渡给阶段 2 的证据决策。
- 标记语法是新增长期契约：解析门闸在立法期强制校验，坏标记的爆炸半径被限制在「该条款不计量」，不影响注入。
- 回退路径（全程保留）：移除标记即回到纯载体文本——资产表退化为历史审计数据，注入与载体版本化行为与现状逐字节一致；不需要迁移回滚脚本以外的任何代码回退。
- 阶段 2 预置否决权：触发条件不满足或证据不足时，阶段 1 形态即为长期形态，不构成技术债（索引与存储同源）。

## 评审记录

（共识评审进行中：覆盖产品语义闭环、数据模型与审计一致性、运行时性能与确定性、统计归因成立性、与立法/自进化体系兼容成本、UI/运维/可观测、失败模式与回滚七个视角；结论与被否决方案在此沉淀。）
