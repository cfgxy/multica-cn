# 如意工坊提示词治理全量回溯与现状核验

日期：2026年11月02日

## 结论

当前体系不是“整体失效”，而是已经覆盖大部分稳定行为合同，但仍存在六类确定性缺口：

1. 动态条款仍把多个独立规则合并在同一版本生命周期，RUYI-915 的结构性根因尚未消除。
2. 蔡小星每轮都需要的 Leader 规则仍有一部分放在仅 `is_leader_task=true` 时注入的 squad scope，实际后续 Run 覆盖不稳定。
3. 平台内建 Squad Operating Protocol 强制“必须派发、派发后立即结束、通常不得自己实现”，与动态条款的单 Run 直办、接管和负责到底正面冲突。
4. RUYI Agent 实际加载的 `guxy-harness-update-prompt` 平台 Skill 仍是旧 Git `snapshot/restore` 路径，与 Owner 已确认的平台真源原则冲突。
5. 知识检索触发条件在静态人物层与 squad clause 之间不一致。
6. 决策卡 v7 已真实注入，但新卡仍出现五维缺项和“整卡 600 字”口径未落实，说明文本落地不等于行为稳定。

没有发现需要恢复整套旧版 prompt 的依据。旧体系同时包含已撤销的强制复盘、绝对 mention、旧资源池门禁、旧 Git 真源等内容；正确方向是保留当前分层架构，按独立失败域修补少数条款和 Skill，而不是整体回滚。

### 2026年11月03日修复状态

Owner 随后授权修复全部 P1、P2-1 和相关 Skill。生产数据库侧已完成：

- 4 个混合 Clause 退役，拆为 `cls_10000064`～`cls_10000078` 共 15 个原子 Clause；历史版本和 withdrawn binding 保留。
- Leader 每轮必需的职责路由、知识判断、防拆单、阶段交付/状态、轮次经济、接管阈值、完成判据和等待管理均改为蔡小星 agent scope，`task_kinds=["issue"]`。
- 知识检索统一为遇阻、重复/已知问题、既有裁定或高风险/高成本探索时触发；每轮只强制判断是否值得沉淀，不强制检索或写入。
- 决策卡改为独立 Clause，并明确五键、`unknowns`、整卡 600 Unicode 字符的统计范围和证据标识豁免。
- 平台 `guxy-harness-update-prompt` 更新到完整 v7，正文及 supporting files 改为平台真源协议；旧 `snapshot/restore` 发布入口已从实际下发 Skill 删除。

代码侧已将 Squad Operating Protocol 改为 direct/delegate/no-action 三路径，移除无条件派发和派发后停工；在本分支测试通过，但需本次 PR 合并、重建 daemon 后才进入生产 runtime。因此下文 P1-1、P1-2、P1-3、P1-5、P1-6 已关闭，P1-4 为“代码已修、待合并部署”。

## 证据范围

本次只读核查覆盖：

- 如意工坊 workspace：`42f61469-c332-41f3-a2b4-018667473993`。
- 914 条 RUYI Issue、19,731 条未折叠评论，其中 2,560 条 Owner/member 评论。
- ZCode `db.sqlite`：4,549 个会话、892,983 个 part，覆盖 2026年09月26日至10月23日。
- Claude Code 本地 5,524 个 JSONL，会话覆盖至 2026年11月02日。
- `ruyi.git` 本机全部可达历史、关键分支及治理文档。
- HP Server 生产数据库当前静态业务列、`prompt_version`、active clause/binding/version、`agent_skill`、`skill_version`。
- RUYI-915 于 2026年11月02日产生的真实 `agent_task_queue`、`clause_injection` 和运行时 `CLAUDE.md`。
- 当前 Multica 源码中的 claim-time 组装、动态条款筛选及 runtime brief 命令清单。

边界：本报告不把 Agent 评论中的“已完成”当作 Owner 批复，不把 Git 文件缺失当作生产丢失，也不把 Issue 状态 `done` 当作所有生产 E2E 已完成。

## 一、治理演进主线

### 1. 早期协作纪律：调度、mention、复盘

代表 Issue：RUYI-4、22、24、25、48、53、77。

形成的长期要求：

- mention 是机器调度动作，不是称呼或抄送。
- 排队消息必须先读取处理，再 consume，不能先消费导致后续指令失去兜底。
- 汇报结论先行，专业、正式、简明，但不能简略成不可理解的“天书”。
- Leader 需要总结协作得失，但后来已撤销机械化、事前阻断式完整复盘。

### 2. 多空间与层级治理

代表 Issue：RUYI-50、66、128、142、144、145、173、175、187、194、195、197。

形成的长期要求：

- workspace 只放跨项目规则，项目事实放 project，角色底色放 agent，队内编排放 squad，操作流程放 Skill。
- 相同规则只在合适层级出现一次，禁止跨空间、跨项目污染。
- 平台写入必须显式定向并独立回读；但旧 Git 仓库作为发布/恢复入口的路径已被后续废止。
- 单个 Issue 能闭环的工作不得为形式分工或规避责任而拆单。

### 3. 自进化、评测和治理产品化

代表 Issue：RUYI-179、183、184、185、186、265、281、285、286、287、288、289、298、305、307、347、367。

形成的长期要求：

- 质量变化要可度量，但指标、条数和 token 下降不能替代真实行为回归。
- 评测必须实际注入被测 prompt；此前只记录 hash、未注入 prompt 的早期分数不能作为治理证据。
- 真实执行中的经验和失败都可成为改进输入，但不恢复七分栏、逐单催收式旧复盘流程。

### 4. 决策卡与结构化协作

代表 Issue：RUYI-345、471、514、580、620、713、714、719、770、862、914、915。

形成的长期要求：

- 真正改变 Owner 已定目标、范围、验收、授权、不可逆取舍或风险承担时，使用结构化决策卡。
- 普通评论中的 A/B/C 不能冒充卡片。
- 卡片需有 `why_now`、`facts`、`impact`、`recommendation_reason`、`unknowns`，但表达必须专业、正式、简明。
- 已定方向、普通技术执行、低风险可逆操作由责任角色自行收敛，不能滥开卡上推 Owner。
- 平台 API 只硬要求标题和选项；五维质量不应被重新解释为所有字段缺失时服务端一律拒绝。

### 5. 动态条款资产化与静态/动态分离

代表 Issue：RUYI-733、737、751、764、765、766、767、768、769、830、834、849、868、869、880、892、899、902。

形成的长期要求：

- 静态层保留人物底色、长期授权、核心原则和稳定工作边界。
- 动态条款承载可版本化、按 scope/task kind 注入的运行期合同。
- 动态条款应按独立规则管理，不保留混合 Clause，不以静态/动态双写长期维持行为。
- 修改已有规则应追加同一 Clause 的版本、保留绑定和审计；有资产不等于已启用，有绑定不等于实际注入。
- 角色人物原文不得因精简被缩写、改写或删除。

### 6. 主动闭环与能力恢复

代表 Issue：RUYI-217、733、798、821、834、867、891、898、903、911、912、913、916、917。

形成的长期要求：

- “纪律防越权、不防思考”；分派只规定目标、边界和验收，不替成员规定实现路径。
- 职责边界不是能力边界。Leader 不能退化成传话筒，QA 不能退化成被动点测。
- 可读事实自行补齐；普通工程问题、环境诊断和授权内替代验证不退给 Owner。
- 只有核心 AC 未完成、授权内路径穷尽、外部依赖证据明确时，整单才 `blocked`。

### 7. P0/P1/P2 与资源生命周期

代表 Issue：RUYI-515、551、555、584、586、589、676～683、695、820、852、853、867、891、903、911。

稳定口径：

- P0：原始需求、主业务路径、数据/权限/安全红线失效，本单必须修复。
- P1：当前 delta 触及的重要关联接缝，本单必须回归并修复。
- P2：边角体验、可选优化、无当前 delta 因果的推演或补证；小改动经 delta Review 直通，大改动在 `in_review` 交 Owner 决定。
- 已恢复的网络、代理、平台评论投递噪声不作为当前代码失败或交付缺件。
- 环境只为仍依赖现场的 P0/P1 保留；P2、历史补证和无 delta 推演不无限占用或重建资源。

### 8. RUYI-915 后续恢复

2026年11月02日，RUYI-915 又恢复两项历史要求：

- `cls_10000015` v7：可在本单闭环的事项不形式拆单；Owner 同意前不建子 Issue、不改变责任边界或指派承接人；建议拆单不阻塞现有工作；批准后子 Issue 由 RUYI squad 承接。
- `cls_10000004` v4：Commit message、PR/MR 标题和描述、Issue 标题和描述默认使用简体中文；代码、命令、配置键、协议字段和固有名称保留原样。

两项均有版本、binding、audit log 和真实注入记录。

## 二、Owner 长期稳定关注点

| # | 稳定要求 | 当前正确理解 |
|---|---|---|
| 1 | 原始诉求和 AC 是交付边界 | 环境发现、平台缺陷和技术债不自动成为本单新前置。 |
| 2 | 静态人物层不能为省 token 被削空 | 人物底色、专业背景、判断方式和长期边界保留在 agent static prompt。 |
| 3 | 纪律防越权，不防思考 | Prompt 规定边界和验收，不规定完整执行步骤。 |
| 4 | Leader 不是传话筒 | 可读事实自行补齐，必要时微幅直办，反复无进展后接管收束。 |
| 5 | Leader 每轮要复盘派发价值 | 评价上一轮覆盖、角色匹配、证据质量和继续派发边际价值；不是每轮写复盘评论。 |
| 6 | Leader 每轮回报后判断是否有知识可沉淀 | 每轮判断，只有已验证、跨 Issue 可复用且有适用边界的内容才写 bd Memory。 |
| 7 | 不轻易拆单 | 只能建议，Owner 同意后再拆；建议不阻塞原单可推进工作。 |
| 8 | P0/P1 本单闭环，P2 不扩大验收 | P2 小修走 delta Review，大改由 Owner 决定留单或新单。 |
| 9 | mention 只服务真实交接 | 成员阶段交付给 Leader 是合法交接；无下一步则静默，不 self-mention。 |
| 10 | 小队 Issue 状态由 Leader 管理 | 成员完成或内部 QA 交接不等于整单 `in_review`。 |
| 11 | 经验先复用，再探索 | 遇阻、重复问题、既有裁定或高成本探索前检索；知识无答案不构成阻塞。 |
| 12 | Skill 需要场景化触发索引 | Prompt 放触发入口，Skill 放操作规范，MCP schema 放接口；实际绑定和 runtime 生效都要核验。 |
| 13 | 决策卡既完整又简洁 | 五维讲清问题，专业、正式、简明；不把常规技术责任上推。 |
| 14 | 交付需要可审查成果 | Run completed、代码写完、局部测试、Draft PR、PR 合并、部署和线上验证分别报告。 |
| 15 | 开发、Review、独立 QA、发布分账 | Leader 接管不绕过独立 QA、安全、权限或不可逆操作。 |
| 16 | 汇报脱离线程也能读懂 | 一句话背景/结论、关键证据、风险/未完成项和真正需要关注的下一步。 |
| 17 | 默认中文交付文本 | Commit、PR/MR、Issue 标题和描述使用简体中文；技术标识保留原样。 |
| 18 | 平台当前内容是提示词真源 | 业务列和 active Clause/binding 是生效源；版本表是历史账本；旧 ruyi Git 不再发布或恢复。 |
| 19 | 区分代码、部署、启用、注入和行为通过 | 任一层完成都不能替代下一层证据。 |
| 20 | 跨空间移植保留固有差异 | 通用合同可迁移，项目/环境/角色差异不得被全量覆盖。 |

## 三、后续已证明不合适、应排除的内容

| 应排除内容 | 后续有效口径 |
|---|---|
| 每条 Agent 评论都必须 mention | 只有真实下一棒可立即行动时 mention；无下一步静默。 |
| self-mention 用于继续当前 Run | 禁止；有工作继续做，完成/阻塞/无下一步按事实结束。 |
| 禁止出现“负责人”等关键词 | 名字不是问题，校验实际目标 ID、职责和下一动作。 |
| 评论正文正则识别“疑似决策”并硬拒绝 | 文本识别最多用于辅助审计；正式结构化卡由行为合同引导。 |
| 五维全部设为 API 硬必填 | 内容质量由 prompt/eval/lint 保障，不能因辅助字段缺失阻断卡片基本能力。 |
| 每单结束前强制七分栏复盘 | 改为轻量、按需、与执行同轮的经验判断。 |
| 每次解决问题立即写知识 | 先反证和复核根因；错误、临时、推测或已失效路径不得固化。 |
| 每个 Issue 开工都全库检索 | 只在遇阻、重复/已知问题、引用既有裁定或高风险/高成本探索时检索。 |
| 内部阶段完成即转 `in_review` | 只有整单具备 Owner 审阅条件才转。 |
| 有有限选项就开决策卡 | 只在改变 Owner 既定边界时开卡。 |
| 旧 ruyi Git 是提示词真源 | 平台当前 business columns 与 active Clauses 是真源。 |
| 静态 prompt 与动态规则长期双写 | 静态人物与动态运行规则彻底分层。 |
| 所有任务必须额外独立环境 QA | 按风险、授权和 Owner 针对本单的例外决定，不能自行增加前置。 |
| 所有任务自动免 QA | 针对单个任务的 Review/思想实验例外不能外推。 |
| 资源池必须受管 exec、DEV/QA Provider 阶段门禁 | 当前为租约、VM、SSH 与受控凭据；Agent 在授权内直接 SSH。 |
| 每个 Run 结束都销毁 VM/AVD/fixture | 未完成且下一阶段依赖时保留、续租、交接；不再需要时主动释放。 |
| “不得泄漏验证码”＝隔离测试不能读取验证码 | 可通过正常发码、受控日志/隔离库取码、正常验码；不得发布或绕过认证。 |
| “第三代架构”是每个 Prompt/Skill 的版本号 | 它只描述整体演进；每个资产以自身生产版本为准。 |
| Token/字符下降就是治理成功 | 必须通过历史行为合同和真实 claim 回归。 |
| Git 中规则消失就是生产规则丢失 | 10月24日后必须查 Clause 版本、binding 和 injection。 |
| 条款 marker/资产存在就是实际生效 | 必须继续核对生命周期、binding、task kind 和真实 injection。 |
| 早期 prompt 评测分数可直接证明优劣 | 未实际注入被测 prompt 的旧分数无效。 |
| RUYI-915 的非 Leader task 缺 squad clause 是平台注入 Bug | 该行为符合当前注入契约；需要判断的是规则是否放错 scope，而不是伪报注入失败。 |

## 四、当前运行时架构核验

### 1. 静态提示词

当前 workspace、RUYI squad、七个核心 Agent 的业务列内容与各自最新 `prompt_version` SHA256 一致。七个 Agent 均保留角色背景、行事风格和稳定边界；没有再次出现蔡小星只剩几句调度规则的过度精简。

### 2. 动态条款

2026年11月03日拆分后共有 33 条 active clause，均有 active binding、明确 scope 和版本；active 数量增加来自原子化拆分，不代表新增行为：

- Workspace：Owner、安全、mention、决策卡、证据标识、中文交付文本、P0/P1/P2、跨角色交付、代码连续交付、连续性和项目事实边界。
- Squad：共享现场；原混合路由/知识/拆单/阶段/经济/接管条款已退役。
- 蔡小星 Agent：职责路由、知识复用与沉淀、防拆单、阶段交付/状态、轮次经济、接管阈值、完成判据和等待管理；其所有 issue Run 均可获得。
- 其他 Agent：研发、QA、PM、设计、发布、巡检合同。
- Multica Project：仓库、目录、基础设施和操作规范。

Task kind 已分流：Issue 工作流不再进入 quick-create；角色和 squad 规则只进入 issue；项目工程规则进入 issue/chat/autopilot。

### 3. RUYI-915 真实 claim

实际任务 `01a19c8d-40b5-7131-9a77-f9f0e700a672`：

- 注入 `cls_10000002` v7，`degraded=false`。
- runtime `CLAUDE.md` 同时包含 `multica issue decision list/add`。
- 注入 Agent clause `cls_10000040` v7 与 workspace clauses。

实际任务 `01a19c6e-4942-7e9f-ba86-b8282484ce93`：

- `is_leader_task=true`，额外注入 `cls_10000015` v7、`cls_10000018` v6、`cls_10000020` v1、`cls_10000021` v1。

因此，RUYI-915 的“条款 v7 没注入、runtime brief 没命令”已经被真实 Run 证据关闭。以上任务发生在原子化拆分前；下一次真实 claim 应进一步确认 `cls_10000064`～`cls_10000078` 的新选择集和蔡小星普通 Run 的 agent-scope Leader 合同。

### 4. Skill 绑定

当前角色启停关系大体正确：

- 蔡小星：提示词治理、ADR、VM/AVD/联合 E2E。
- 顾小鱼：后端调试、Bugfix、VM/AVD/联合 E2E。
- 金小欣：只读 Bug 分析、VM/AVD/联合 E2E；实现型 Bugfix 已停用。
- 白小巡：只读 Bug 分析；资源写操作 Skill 已停用。

资源 Skill 已去除 RUYI-903 专属标题，并包含 P0/P1、P2、ENV-FAULT、续租和回收边界。

## 五、逐项覆盖矩阵

| 要求 | 当前落点 | 状态 | 说明 |
|---|---|---|---|
| 静态人物底色 | 七个 Agent business column | 通过 | 与最新版本哈希一致。 |
| Owner 最高指挥权 | `cls_10000001` v2 | 通过 | 高风险仍需一次性确认。 |
| P0/P1/P2 | `cls_10000068` v1、40/31/46 | 通过 | 已恢复噪声不扩大验收。 |
| mention/静默/self-mention | `cls_10000064` v1 + 平台门禁 | 通过 | 与决策卡已分离。 |
| 结构化决策卡命令 | `cls_10000065` v1 + runtime brief | 通过 | 独立 Clause；命令链已由历史真实 claim 验证。 |
| 决策卡五维和简洁度 | `cls_10000065` v1 | 规则已修 | 明确五键、`unknowns` 和 600 Unicode 字符口径；待下一张新卡行为验证。 |
| Leader 每轮评价派发 | `cls_10000040` v7 | 通过 | Agent scope，每个蔡小星 issue task 可见。 |
| Token/run 经济采样 | `cls_10000075` v1 | 通过 | 已迁到蔡小星 agent scope。 |
| 三轮无进展/两次不满接管阈值 | `cls_10000076` v1 | 通过 | 已迁到蔡小星 agent scope。 |
| Leader 直办/接管权限 | Agent clauses + 内建 squad briefing | 代码已修 | 内建协议已改 direct/delegate/no-action；待 PR 合并、重建 daemon。 |
| 每轮回报后判断知识沉淀 | `cls_10000071` v1 | 通过 | 每轮判断，不强制每轮写入。 |
| 遇阻优先检索 | Agent static + `cls_10000071` v1 | 通过 | 已统一为场景触发，不再每次调度前强制检索。 |
| Skill 场景索引 | Leader/Dev/QA/巡检 static | 通过 | 角色启停与索引基本一致。 |
| 提示词治理 Skill 当前真源 | 平台 `guxy-harness-update-prompt` v7 | 通过 | description、完整正文和 supporting files 均与本机当前平台真源协议一致。 |
| 不随意拆单 | `cls_10000072` v1 | 规则已修 | 蔡小星所有 issue Run 可见；待完整行为矩阵。 |
| 小队状态由 Leader 管理 | `cls_10000003` v3、`cls_10000074` v1 | 通过 | workspace 层对成员可见，Leader 细则为 agent scope。 |
| PR 持续交付和合并边界 | Leader static、`cls_10000078` v1、Dev clause | 部分 | 连续交付已独立；完整 PR 流程补齐卡仍 open。 |
| 独立 QA | workspace/Leader/QA/Dev | 通过 | 开发自证不替代独立 QA。 |
| 资源生命周期 | `cls_10000007`、20/21 + 三个 Skill | 通过 | P2 不应无限保留现场。 |
| 中文交付文本 | `cls_10000004` v4 | 通过 | 2026-11-02 已版本化恢复。 |
| 项目事实不污染 workspace | `cls_10000010` v2 | 通过 | 项目事实下沉 project/Skill。 |
| 真实 claim 回归 | RUYI-915 | 部分 | 决策卡路径已验证，其余行为合同仍待持续观测。 |
| Decision Center UI E2E | RUYI-915 open card | 待决/待验 | 服务端卡片创建、读取、card_click 历史链路已验证；本轮 UI E2E 未闭环。 |

## 六、当前确定性缺漏

### P1-1：动态条款原子性（已修复）

修复前，`cls_10000002` 同时承载 mention 语义和决策卡规则。v4 含两类内容；v5 为修 mention 语义而替换成 327 字，决策卡规则随之消失；v7 又把决策卡补回同一 Clause。

这说明 RUYI-915 修复了正文，却没有隔离失败域。下一次修改 mention 时仍可能再次丢失决策卡。相同问题还存在于：

- `cls_10000015`：路由、知识、拆单、阶段交付共用一个版本。
- `cls_10000018`：状态、token/run 经济、接管、完成判据共用一个版本。
- `cls_10000004`：证据、中文文本、P0/P1/P2、附件交接共用一个版本。

混合条款还会使 clause quality 的归因粒度失真。该现状与 RUYI-869“无混合 Clause、规则独立版本化”的要求存在冲突。

修复结果：四个混合资产已保留历史并退役 active binding，分别拆为 `cls_10000064`～`cls_10000078`；每条新资产只有一个主题、独立 v1、active binding 和审计记录。`cls_10000021` 保留原正文版本，仅迁移 binding scope。

### P1-2：平台提示词治理 Skill 传播废弃运维路径（已修复）

修复前，Agent 实际加载的 workspace Skill `guxy-harness-update-prompt`：

- 当前平台版本：v4。
- 版本时间：2026-10-20 08:31。
- 正文仍写“本机提示词仓库”“snapshot/restore”“仓库→平台回写”。
- 描述触发词仍含“提示词快照、回写平台、多空间同步”。
- runtime 文件已在 RUYI-915 工作目录中实证。

但 Owner 在 RUYI-915 2026-10-31 10:05 已明确：Multica 当前保存的提示词和动态条款是权威版本，旧 ruyi Git 不再作为维护、发布或恢复入口。

本机 `~/.claude/skills` 已更新，不代表 RUYI Agent 加载的 workspace Skill 已更新；修复前该状态构成确定性 source-of-truth 冲突。

修复结果：平台 Skill 已追加完整 v7；description SHA256 为 `d7d46d1cc6ad...`，正文为 `27b08bdf107a...`，`defect-patterns.md` 为 `0bac74eebd8b...`，`repo-ops.md` 为 `9f427c1d916d...`。实际业务列和 supporting files 已独立回读，不再包含 `snapshot.sh` 发布入口。v5/v6 是修正正文截断和 description 精确一致性的 append-only 中间版本。

### P1-3：Leader 核心规则 scope（已修复）

修复前，2026年11月02日 RUYI-915 的蔡小星任务：

- `is_leader_task=true` 且已执行：6 次。
- `is_leader_task=false` 且已执行：11 次；另有 1 次启动前取消，不计入运行时缺席样本。

非 Leader task 按当前平台契约不会注入 squad clauses，这不是注入实现 Bug；但这些任务仍是蔡小星对 squad Issue 的后续调度、Review 和收束 Run。

结果是 11 次实际执行的普通 Run 看不到：

- `cls_10000015` 的防拆单、知识沉淀时机和阶段交付规则。
- `cls_10000018` 的 token/run 采样点、三轮无阶段交付接管阈值和完成判据。
- `cls_10000021` 的无状态变化时禁止重复派发规则。

Agent clause `cls_10000040` 保留了通用“每轮评价”和“接管触发后”规则，但未包含具体接管阈值、经济采样和每轮知识判断。因此行为合同随 task 产生方式变化。RUYI-915 还出现过顾小鱼无新事实 mention 蔡小星、蔡小星再 mention 顾小鱼确认等待的真实互唤；workspace mention 规则始终在场，说明这里既有 scope 缺口，也有已注入规则未被稳定执行，不能把全部责任归给漏注入。

修复结果：职责路由、知识、防拆单、阶段交付/状态、轮次经济、接管阈值和完成判据均拆为蔡小星 agent clauses；等待管理 `cls_10000021` 也从 squad binding 迁到 agent binding。普通和 Leader issue Run 的有效 agent 选择集一致。

### P1-4：内建 Squad Operating Protocol 与直办/接管合同冲突（代码已修，待部署）

修复前，平台在所有 squad Leader task 前置硬编码协议：

- 即使直接请求 Leader 做某项工作，也“must delegate”。
- 派发评论和 activity 记录后必须立即结束，不继续工作、不开文件。
- 除非没有合适成员，否则 Leader 不得自己实现。

对应源码为 `server/internal/handler/squad_briefing.go` 的 `squadOperatingProtocolHeader` 和 `squadOperatingProtocolHardRules`。

这与修复前的动态合同同时竞争：

- `cls_10000015` v7 要求单次 Run 可闭环时 Leader 直接收束。
- `cls_10000018` v6 要求触发阈值后接管全部授权内余项。
- `cls_10000040` v7 允许满足条件的微幅直办。
- Owner 长期要求 Leader 不能退化为传话筒、调度器。

该冲突位于内建 system briefing 与动态条款之间，单改 Clause 措辞无法彻底解决；需要对内建“必须派发/派发后停止”协议与直办、接管权限做统一裁决。

修复结果：内建协议已改为 direct/delegate/no-action 三路径；直办限定 Leader-owned、授权微幅任务或 active takeover，且不豁免独立 QA、安全、授权和不可逆边界。派发后只禁止重复成员工作，不再强制立即停工；最终 activity 在所选路径完成或失败后记录，未完成的 direct work 记 `failed`。quick-create 使用专用 create-only 协议，不再接收 issue-bound activity/status/comment/mention 要求。代码测试已通过，生产生效仍依赖 PR 合并和 daemon 重建。

### P1-5：知识检索触发条件前后不一致（已修复）

修复前，Agent static 的口径是：遇阻、重复/已知问题、引用既有裁定或高风险/高成本探索时检索；目标明确的普通任务不做形式搜索。

修复前 `cls_10000015` v7 的口径是：每次调度、交付或升级前先检索历史 Issue、裁定和知识库。

后者重新扩大为高频前置，与 2026-11-01 审计所称“知识检索已收窄”不一致，会增加空形式检索和上下文税。

修复结果：`cls_10000071` v1 与静态口径统一；每轮只要求判断是否值得沉淀，检索仍按遇阻、重复/已知问题、既有裁定或高成本探索触发。

### P1-6：决策卡行为（规则已修，待新卡验证）

v7 生效后创建的两张 open 卡：

- Decision Center UI E2E 卡 `01a19bf8-9d00-7cc4-9848-9cdba48f6531`：五键完整；整卡展示文本 647 字符。
- PR 流程补齐卡 `01a19c7a-ca72-7cfc-acff-43eae25bbfd6`：缺 `unknowns`；整卡展示文本 699 字符。

两卡各现有字段均基本满足 1～3 句，但都超过“整卡不超过 600 字”的自然理解，且“600 字”究竟统计 context、汉字还是问题＋选项＋context 没有定义。Prompt 已被模型部分遵守，尚未形成稳定行为。

修复结果：独立 `cls_10000065` v1 明确问题、全部选项和 `context` 面向 Owner 的正文合计不超过 600 个 Unicode 字符；证据中的 URL、Issue/PR 标识和 commit SHA 不计。`unknowns` 必须存在，无未知项使用空数组。既有 open 卡不改写，下一张新卡验证行为。

### P2-1：治理审计文档过时与内部矛盾（已修复）

`docs/ruyi-prompt-governance-audit-2026-11-01.md`：

- 仍写 runtime brief 未提交、未合并、未部署；实际 PR #550 已于 23:31 合并，二进制已重建，并有真实 claim 注入证据。
- “知识与 Skill 发现”仍写 QA 使用实现型 Bugfix、白小巡使用资源 Skill；后文又写这些已停用。
- 仍写“下一次真实 claim 应验证”；真实 claim 已发生。
- 未记录 2026年11月02日 anti-split 和中文规则恢复，以及两张 open 决策卡。

修复结果：旧审计已修正 Skill 启停描述和 PR #550/真实 claim 状态，并追加 2026年11月03日原子 Clause、Leader scope、治理 Skill v7 与内建协议代码修复记录；历史当时数字保留为历史，不再冒充当前状态。

### P2-2：项目动态层仍有明显常驻 token 税

Multica project 四条 active clause 合计约 6,886 字符，并同时注入 issue/chat/autopilot。当前没有证据支持直接删除；但应按真实调用频次和行为回归继续评估哪些工程细则可以下沉 Skill。不得再用纯字符缩减代替能力保真。

### P2-3：行为回归仍不完整

已经有真实证据的项目：

- 决策卡 v7 和 runtime 命令进入真实 Run。
- anti-split v7、中文 v4 进入 Leader task。

尚缺系统性真实/隔离回归：

- 直接后续 Leader Run 的防拆单、接管、知识沉淀。
- P0/P1/P2 结束资源验证。
- 成员阶段回报与 Leader 状态管理。
- QA scoped FAIL/ENV-FAULT。
- Skill 场景触发及实际消费 bd Memory。
- 完整 PR Draft→Ready→QA/Review→Owner merge 交付链。

## 七、RUYI-915 当前状态

已关闭或已落地：

- 决策卡规则与 mention 已拆为独立 `cls_10000065` 和 `cls_10000064`。
- runtime brief 已包含 `multica issue decision list/add`。
- 防拆单、中文交付文本、知识沉淀、轮次经济、接管阈值和阶段完成规则均已原子化并调整到稳定 scope。
- 平台实际下发的治理 Skill 已升级到完整 v7。
- 内建 Squad Operating Protocol 已在代码中修订并通过测试。

仍未闭环：

- 内建协议代码尚需本次 PR 合并、重建 daemon 和真实 Leader claim 验证。
- 原子 Clause 新选择集需要下一次真实普通/Leader claim 回读。
- PR 全流程和 Decision Center UI E2E 仍服从 Owner 对 RUYI-915 既有 open 卡的裁定，不在本次治理中抢跑。
- 防拆单、接管、知识沉淀和决策卡简洁度仍需行为矩阵，而不能以数据库回读代替。

## 八、后续验证顺序

1. 合并本次代码 PR 并重建 daemon，确认 Leader runtime 出现 direct/delegate/no-action 协议且不再出现绝对“must delegate/stop after dispatching”。
2. 触发一个蔡小星普通 issue Run 和一个 squad Leader Run，回读 `clause_injection`；两者均应获得 agent-scope Leader 原子条款，Leader Run 额外只获得真正的 squad 载体。
3. 用下一张真实决策卡验证五键、`unknowns` 和 600 Unicode 字符口径；既有 open 卡不回写。
4. 回归防拆单、三轮无进展接管、无变化等待、每轮知识判断与 P0/P1/P2 收束。
5. 等待 RUYI-915 既有两张卡的 Owner 裁定；PR 全流程和 UI E2E 不抢跑立法。

## 九、关键历史证据索引

### 关键 Git SHA

- 引用化过度压缩后恢复：`c59d1c51328b57b0fcdc506e7fdaa11d6d47c644`
- handbook 迁移：`856fd295ad693983c298b066f667a676e3e6c06a`
- 防删登记及背景恢复：`0b48b2690c5abde84b944d1332ff76ba03012f7a`
- 10月大精简：`3d5890fc58ab42e7dfe3d04925dbbf959a5e6285`
- Leader/角色能力恢复：`8d9e7c335ac147c020842da8bba7dee06594eea5`
- 主动闭环恢复：`449ba11b347d0dd486d4dcaf3b837778c8f2a005`
- 评测实际注入修复：`078b8eeafda9ab30d2fc29a3b0bfff863bd2ff2d`
- 动态迁移初始盘点：`2d417afae239662f5b4c4fc76cec400393c08842`
- 四级静态化：`8fcb9ffd3e09a5b952a9930de032b43caa11e254`
- 回写门闸调整：`da3dd797eff145c38cdf38fe79e596a81e4de5aa`

### 重点 Issue 组

- 早期协作：RUYI-4、22、24、25、48、53、77。
- 层级/空间/拆单：RUYI-50、66、128、145、173、175、187、194、195、197。
- 自进化/评测：RUYI-179、183～186、265、281、285～289、298、305、307、347、367。
- 决策卡：RUYI-345、471、514、580、620、713、714、719、770、862、914、915。
- 动态条款：RUYI-733、737、751、764～769、830、834、849、868、869、880、892、899、902。
- 主动闭环/mention：RUYI-798、821、834、867、879、881、900、903、911、912、917。
- Skill/资源：RUYI-366、421、515、551、555、584、586、589、676～683、695、820、852、853、898、903。
- 知识治理：RUYI-911、913、916。
- 交付/PR：RUYI-191、671、733、790、793、802、809、842、905、915、918、919。

## 十、审计边界

2026年11月02日的审计阶段未修改任何平台对象或代码；2026年11月03日经 Owner 明确要求“修 P1 + P2-1 + 相关 Skill”后，按本报告结论执行了版本化 Clause/Skill 写入和代码修复，未修改 Issue 内容或既有决策卡。所有结论与修复记录均区分：

- 历史要求与当前有效要求；
- Owner 明确指令与 Agent 落地声称；
- 静态文本、动态条款、Skill 和 runtime brief；
- 已写入、已绑定、已注入和行为通过；
- 规则缺失、规则冲突、措辞歧义及平台契约本身。
