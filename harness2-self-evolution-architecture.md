# ADR-010：自进化 Harness 实验工作区

| 属性 | 值 |
|---|---|
| 创建日期 | 2026-10-22 |
| 归档日期 |  |
| 最后核验日期 | 2026-10-22 |
| 状态 | proposed |
| 仓库 | cfgxy/multica-cn |
| 来源 | RUYI-846、产品规划、UI/交互设计稿 |

---

## 1. 背景与范围

工作区需要在既有“自进化”模块内实验 Harness 的任务内策略适配；产品、交互和架构均要求它不替代 Prompt 版本、质量、题库、提案审批和回滚。已核验的 `main` 基线为 `fdaa42a1122127791e6f61c58f9d9fb5154a749e`：Web 已有 `self-evolution` 子路由，服务端已有 retrospective runner 与 proposal legislation 链路。

纳入 P1：实验定义、同一固定任务集上的基线/候选配对运行、运行快照和证据/指标展示、停止与重试。P1 只启用任务级临时适配。

不纳入 P1：跨任务经验自动提炼、自动修改共享 Prompt/Skill、全局运行时设置、平台队列或权限模型改造、通用 A/B 平台和生产数据迁移。跨任务模式和“提交候选提案”在 P2 使用同一证据模型接入，不能被 P1 的完成状态隐式授权。

## 2. 决策

### 决策 1：原生实验域，不嵌入外部 Harness 实现

在自进化模块新增 `experiment` 域，按本项目的 Agent、Prompt、题库和提案契约实现“提出候选 -> 配对执行 -> 记录证据 -> 判定”的流程。只吸收方法，不运行或嵌入外部工程。

收益是与既有身份、审计和 UI 一致；代价是需自行实现最小编排。它避免把外部执行器、网络策略或产物格式带入平台。

### 决策 2：新增两类持久化对象，运行记录不可变

`self_evolution_experiment` 保存可编辑的实验定义：workspace、名称、假设、模式、基线/候选引用、固定任务集引用、预算和评估规则，以及创建/更新时间。

`self_evolution_experiment_run` 保存一次运行：experiment ID、递增 run number、状态、执行任务 ID、开始/结束时间、不可变 `snapshot`、`progress`、`evidence`、`summary` 和已脱敏错误摘要。策略版本、任务集修订、模型和预算在 `snapshot` 冻结；样本结果、指标和判定写入 JSON 证据，不另建任务/指标/策略表。

这使重试产生独立运行而不覆盖历史，且以两张新增核心表完成首期可追溯性。对列表的摘要字段可在 run 完成时物化到 run 行，不能由 UI 从原始证据推算。

### 决策 3：复用既有 Agent 任务执行通道，但不改变核心合约

`ExperimentRunner` 是自进化域服务，创建 run 后通过一个与 retrospective task enqueuer 同形的窄适配器提交现有 Agent 任务。它不改变队列协议、daemon/runtime、工作区权限或全局模型设置；任务上下文只含 `experiment_run_id`、冻结快照和结构化回传约束。

回传必须为结构化证据：基线/候选配对结果、样本计数、指标值、守护指标、成本/延迟和脱敏失败原因。服务端校验 run 归属、状态和快照后才落库。未知字段、原始凭据或未脱敏任务正文不进入证据。

### 决策 4：状态机和权限固定在服务端

```text
实验：draft -> ready -> archived
运行：queued -> running -> evaluating -> completed
                         |              |
                         v              v
                    failed / stopping -> stopped
```

只有 Owner 可创建、编辑、启动、停止或重试；成员仅可读取其已有工作区可见范围。一个实验同一时刻最多一个非终态运行，开始和停止操作以 run ID 幂等。停止先标记 `stopping`，再取消该 run 关联的精确任务；无法确认取消时保留状态和原因，不能伪报已停止。

### 决策 5：先完成任务级适配，受控提升延后

P1 路由为 `/{workspaceSlug}/self-evolution/experiments`、`.../new`、`.../:id`。页面沿现有自进化导航实现列表、分步配置、运行详情和指标比较；跨任务模式与“提交受控提升”显示为未启用能力，不允许启动或生成提案。

P2 才接入跨任务经验候选与现有 proposal 创建接口。入口必须携带来源 run、适用范围、收益/成本、反例和当前策略版本；任一证据缺失、样本不足或对象版本漂移时拒绝创建候选。既有审批、题库防回归和回滚继续是唯一生效路径。

## 3. 接口与数据契约

| 能力 | P1 API 契约 |
|---|---|
| 定义 | `GET/POST /api/self-evolution/experiments`，`GET/PATCH /.../{id}`；PATCH 仅 draft |
| 运行 | `POST /.../{id}/runs` 创建冻结快照；`GET /.../runs/{runId}` 返回权威状态、摘要和脱敏证据 |
| 控制 | `POST /.../runs/{runId}/stop`，`POST /.../runs/{runId}/retry`；重试复制定义而非旧 run 证据 |
| UI | 列表只读摘要；详情以 run 响应呈现状态、样本、指标和停止/失败原因 |

`snapshot` 至少含 `mode`、baseline/candidate 的 ID 与内容 hash、task-set ID 与 revision、执行配置、预算、评估规则和最小样本。`summary` 至少含结论（improved/not_met/insufficient/inconclusive）、有效样本、主指标、守护指标、成本和延迟。所有聚合均由服务端生成，UI 不作通过/失败推断。

## 4. 替代方案

- **直接嵌入或运行 Harness² 工程**：可较快复用其执行器，但会引入不同的运行、网络和产物契约；不选。
- **以独立通用实验平台承载**：可复用给其他产品，但会先引入队列、权限和多租户治理问题；不选。
- **只在前端拼接现有数据**：无不可变配置和配对执行证据，无法支撑停止、重试或后续提案；不选。

## 5. 后果、回退与重新评估

该方案新增自进化域迁移、API、runner、Web 页面和测试，不改变既有治理或核心任务接口。回退时关闭实验入口、拒绝新 run、请求停止活动 run，并保留已完成的定义和运行证据；不删除历史数据，也不回滚任何正式 Prompt。

当需要真实未脱敏任务、跨 workspace 经验、运行时/权限合约改动或自动生效时，必须重新评审本 ADR。若 P1 无法通过现有任务通道可靠执行结构化回传，也应停止扩展，而非私自改造核心队列。

## 6. 实施映射与验证

| 交付 | 预期落点 | 实现状态 | 验证状态 |
|---|---|---|---|
| 定义与运行迁移/查询 | server DB migration、query 层 | 未实现 | 未验证 |
| 状态机与任务适配器 | `server/internal/selfevolution/experiment` 或同域位置 | 未实现 | 未验证 |
| HTTP 契约与权限检查 | server handler/router | 未实现 | 未验证 |
| 实验页面 | `apps/web/.../self-evolution/experiments` | 未实现 | 未验证 |
| 测试 | server 单元/handler，Web 交互与可访问性 | 未实现 | 未验证 |

P1 验收：Owner 能创建任务级实验并得到不可变 run 快照；同一 run 的基线/候选使用相同任务集修订；停止/重试不覆盖历史；服务端拒绝越权、无效转换和证据缺失；UI 正确显示 `insufficient`/`inconclusive` 而不显示通过；成员不能执行写操作；现有自进化、proposal、题库和版本回滚回归不受影响。

## 7. 关联 ADR

- ADR-007：Clause 资产生命周期与注入；P2 只能经其既有治理路径形成候选。
- ADR-008：运行活动可观测性；实验 run 应复用而非平行定义核心运行观测语义。

