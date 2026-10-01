# RUYI-292 技术方案：Run 生命周期管理闭环（v1）

- 作者：蔡小星（架构师/Leader），2026-09-29
- 行为规格基线：谢小婷产品规格评论 `01a0ed63-8e3e-7ebc-933c-0f40ba6a3f7b` 第三~六节（本方案不重复产品论证，只做工程裁定）
- 现状锚点核验：`deb660e2d2c65973d02753d86e9a05ea3ec85825`（origin/main tip `92fe5c7b8de8d94f40feabc6d53580735a3d0f0b` 的直接祖先，已核对）

## 1. 目标与范围

补齐 Issue → Runs → Run 操作 → Runtime 执行 → 历史追溯闭环，覆盖 MCP 与 UI。范围 = Issue 描述「能力范围」全部条目 + 验收标准 v2（9 条 + 增补）。不在范围：系统批量取消路径语义改造、会话续跑、Runtime 选型（结论单向反馈 RUYI-280/281）。

## 2. 现状锚点（全部已只读核验）

| 事实 | 位置 |
| --- | --- |
| 状态枚举 + CHECK：queued/dispatched/deferred/waiting_local_directory/running/completed/failed/cancelled；无 cancel_requested | `server/migrations/001/109/255`；`server/pkg/db/generated/models.go`（AgentTaskQueue） |
| 服务端终态判定 / daemon 终态判定 | `server/internal/service/task.go:1083`；`server/internal/daemon/gc.go:833`（isAgentTaskTerminal） |
| 用户单 Run 取消：`POST /api/issues/{id}/tasks/{taskId}/cancel` → `CancelTask` → `CancelTaskByUser` → `CancelTaskWithResult`（事务 + 广播 + chat 结算 + token 回收） | `server/cmd/server/router.go:2166`；`server/internal/handler/daemon.go:5104`；`server/internal/service/task.go:2818/2849` |
| daemon 取消发现：轮询 + reconcile 广播，`shouldInterruptAgent` 命中终态或 task-not-found 即中断 | `server/internal/daemon/daemon.go:5328/5339`（watchTaskCancellation） |
| ack 通道已存在：`POST /tasks/{taskId}/cancel-ack` → `AckTaskCancelled`：现语义为服务端已直翻 cancelled 后，daemon 幂等补写 durable_work_dir/branch/error（status='cancelled' CAS） | `router.go:1580`；`handler/daemon.go:4923` |
| 手动按任务 rerun 已存在：`POST /api/issues/{id}/rerun {task_id}` → 写 `rerun_of_task_id` + `force_fresh_session=true`、新 attribution 归发起者、对历史 Agent 实时 invoke 校验 fail-closed（MUL-4525） | `server/internal/handler/task_lifecycle.go:181`；`server/internal/service/task.go`（RerunIssue） |
| 系统自动重试血缘：`retry_of_task_id` + `attempt`/`max_attempts`/`failure_reason`（两列分账是 MUL-4302 §5 的显式设计） | `models.go` 注释；`server/migrations/055/184` |
| 并发约束：per-(issue,agent) 唯一 partial 索引 v2（queued/dispatched + media-deferred） | `server/migrations/257` |
| 列表：`ListTasksByIssue`（`active=true` / `scope=family`，全量 newest-first；无状态/来源筛选参数） | `handler/daemon.go`（ListTasksByIssue） |
| MCP：11 个工具，无任何 Run 生命周期工具 | `apps/mcp/src/tools.ts` |
| UI：issue 详情 sidebar 已有执行历史；WS 已有 `task:cancelled` 事件 | `packages/views/issues/`；`packages/core/types/events.ts:34` |
| 无取消归属列（models 无 cancelled_by / cancel_requested_*） | `models.go` |

对规格两处细节修正（不改变结论）：

- 取消路由是 issue 域内 `POST /api/issues/{id}/tasks/{taskId}/cancel`，非 `/api/tasks/{id}/cancel`。
- 终态行取消现状并非严格「幂等空操作」：`CancelTaskByUser` 失败路径映射 400、成功路径 200 返回现状行；409 语义是本单新增目标。

## 3. 架构决策（裁定规格第八节四点）

- **D1 `cancel_requested` 落地为持久 status 值**（非标志位组合）。唯一同时满足「重启不丢 + 多端可读」的形态；沿用 109 改 CHECK 的既有模式。拒绝标志位：所有消费方都要派生展示态，多端口径必然漂移。
- **D2 两段式取消仅用于用户单 Run 取消**。系统路径（`CancelTasksForIssue` / `CancelTasksForAgent` / `CancelTasksByTriggerComment` / `CancelTaskWithReason`）维持现状直翻 cancelled——daemon 轮询见终态即中断，该行为已在线上验证。手术式边界：批量路径语义不在本单验收面，登记为后续演进候选。
- **D3 竞态与终态守卫**。所有终态写入走「CAS on 当前状态」，先到者胜：
  - cancel 对 queued：服务端权威直翻 cancelled（无进程在跑）。
  - cancel 对 dispatched/deferred/waiting_local_directory/running：置 cancel_requested + 归属列 + 广播 `task:cancel_requested`。
  - daemon 中断后发 cancel-ack：ack 端点扩展——行处于 cancel_requested 时 CAS 翻转 cancelled 并记 `completed_at`=ack 时间（保留现有 workdir/branch/error 补写与 rebroadcast）；行处于其他状态时维持现有幂等语义（滚动部署向后兼容）。
  - daemon 的 `/complete`、`/fail` 回写落在 cancel_requested 行：守卫拒绝按完成/失败落账，收敛为 cancelled（「取消先到者胜」，对应规格三竞态条）。
  - completed/failed 收到取消 → 409（契约变化点）；cancelled → 200 `already_cancelled`；cancel_requested → 200 `already_cancelling` 并重发 reconcile 广播促 daemon 立即轮询。
- **D4 取消超时 30s（常量入 `packages/core` 共享）**：后端不自动翻转；API 暴露 `cancel_requested_at` 供各端计算；UI/MCP 客户端超阈值展示常驻告警 + 允许重复取消。
- **D5 手动 Retry 复用 rerun 血缘**：不新增血缘列、不与 `retry_of_task_id` 合并（MUL-4302 §5 分账语义保留）；详情祖先链 = 两列 UNION 可视（含跨链分叉）。新增薄入口 `POST /api/issues/{id}/tasks/{taskId}/retry` 调用 RerunIssue service 并叠加规格五的回执语义；既有 `/rerun` 端点行为不变。
- **D6 Retry 防重复三规则**：
  1. 唯一索引冲突（同 Issue+Agent 已有排队）→ 409 `agent_already_queued`；
  2. 同源存在未终结直接后代 → 409 `retry_descendant_active`（UI 置灰）；
  3. 同发起者 5s 窗口内重复 → 200 返回既有后代 Run（MCP 幂等语义）；窗口外命中规则 2。
- **D7 MCP 四工具**：`list_issue_runs(issue_id, status?, trigger?, active?, limit?)` / `get_run(run_id)`（含祖先链 + 取消归属 + 失败原因原文）/ `cancel_run(run_id)` / `retry_run(run_id)`。schema description 显式声明副作用与 403/404/409 语义，与 UI 一套口径。
- **D8 用户可见状态归并**（前端共享常量）：排队中 = queued+deferred+waiting_local_directory+dispatched；运行中 = running；取消中 = cancel_requested；已完成/失败/已取消独立。多端（UI/MCP/CLI）同源。

## 4. 数据模型与迁移

- 编号：磁盘现存 max = `949`；开工前另核对共享开发库 `schema_migrations` 记账 max，取两者更大者 +1（项目 instructions 规则）。
- **950（CHECK）**：`DROP CONSTRAINT` + `ADD CONSTRAINT ... CHECK (status IN (..., 'cancel_requested'))`，两语句一文件；约束交换非索引，不适用 CONCURRENTLY；锁窗口短，必要时 `NOT VALID` + `VALIDATE CONSTRAINT` 分步。
- **951（归属列）**：`ADD COLUMN cancel_requested_by_user_id UUID NULL`、`cancel_requested_at timestamptz NULL`。系统路径取消原因沿用现有 `error`/`failure_reason` 列，不新增。
- 无新表（DeleteWorkspace 级联覆盖无需增补）、无新索引（主键查路径为主；后代查询走两血缘列小结果集，性能需要时再单独 `CONCURRENTLY` 迁移）。
- 回退：down 迁先把存量 cancel_requested 行收敛为 cancelled，再收列/收 CHECK；应用层新状态对旧代码是未知非终态，见 §10 过渡分析。

## 5. 服务端与 daemon 行为规格

- `CancelTaskByUser` 重构为矩阵驱动（规格三全表），响应体统一 `{code, task}`；`already_cancelling`/`already_cancelled`/409 三种回执的人话文案与规格六一致。
- daemon：`shouldInterruptAgent` 增加 cancel_requested；中断路径收敛后 cancel-ack 携带终止确认（ack 体扩展可选字段 `confirmed: true`，旧体 `{}` 兼容）。
- `watchTaskCancellation` 的 poll/reconcile 双通道不变，新增状态自然被现有轮询发现。
- Retry service：源 Run 必须终态（failed/cancelled 必开；completed 沿用现状）→ MUL-4525 历史 Agent invoke 门 → D6 三规则 → 入队（attempt 重置、`force_fresh_session=true`、触发输入 = `trigger_comment_id` 存活则重放，全删则回退 issue 级语义并在详情标注「原始输入已不可恢复」）。
- WS：新增 `task:cancel_requested` 事件（`events.ts` + realtime sync + React Query 失效键）。
- 权限：沿用现状——chat Run 仅创建者；Issue Run 按 Agent 可见性门（`canAccessPrivateAgent`）+ invoke 门；403/404/409 响应体均带人话说明。

## 6. MCP 工具面

`apps/mcp/src/tools.ts` 按现有风格注册 D7 四工具；错误码与人话文案单点定义、UI 与 MCP 共用（避免两套口径）。工具级测试覆盖 403/404/409 各分支。

## 7. UI 面（`packages/views` + i18n）

在 issue 详情既有执行历史上扩展：六态徽标（含「正在停止」+ 再次停止按钮）、状态/触发来源筛选、分页；Run 详情抽屉：祖先链（两血缘列 UNION，含分叉）、取消发起者与时间、失败原因人话映射（agent_error/timeout/runtime_offline/runtime_recovery/manual）+ 原文；取消超时 30s 常驻黄条；空态与「清除筛选」；cancelled 入口文案「重新运行」+ 确认框。文案进 locales（中/英/日/韩按现状）。

## 8. CLI

`issue cancel-task` 输出适配 cancel_requested；新增 `issue retry-task <task-id>`（对齐新 retry 端点；AC 未硬性要求，顺带交付）。

## 9. 测试与验收映射

| 验收点 | 测试 |
| --- | --- |
| AC1 + 增补1 | ListTasksByIssue 筛选参数单测（状态归并/来源/取消中）；UI 组件测试 |
| AC2 | cancel 矩阵行级集成测试（queued 直翻 / running 两段 / 不影响并行 Run） |
| AC3 + 增补3 | E2E 实机：cancel_requested → ack → cancelled 迁移 + 进程树终止证据 |
| AC4 + 增补2/4 | 竞态两向（CAS 先后）、终态 409、重复取消幂等、超时不翻转（伪 ack 缺席）单测 |
| AC5 + 增补5 | cancelled/failed Retry 入队、rerun 血缘、UI 链可视 |
| AC6 + 增补6 | 当前配置非快照断言、新会话断言（force_fresh_session）、409 两码、5s 幂等 |
| AC7 | 403/404/409 响应体断言（API + MCP） |
| AC8 | E2E 实机：双并行 Run 取一留一 + 失败重试成功 |
| AC9 | 实测结论由 QA/开发在交付说明汇总，反馈 RUYI-280/281 |

## 10. 交付切片、风险与回退

- 单分支序列提交：①迁移 + 取消两段式 + 矩阵 ②retry 语义 ③MCP 四工具 ④UI + WS + i18n ⑤E2E。每步 Go 测试绿再进下一步。
- 风险与对策：
  - CHECK 交换锁：表写入窗口短；NOT VALID+VALIDATE 备选。
  - 滚动部署新旧 daemon 混布：旧 daemon 不识别 cancel_requested 不会主动中断，任务跑完后 `/complete` 被 D3 守卫收敛为 cancelled——终止延迟但有界，过渡期可接受；文档记录。
  - 409 契约变化影响既有调用方：CLI cancel-task 文案适配；MCP 无既有 Run 工具，无影响；前端现按 200 幂等处理需同步。
  - E2E 实机依赖 runtime 环境：AC3/8 在 QA 段由金小欣实机复核，开发段以集成测试 + 本地 daemon 冒烟替代。

## 11. 核心性判定与 Owner 否决点

本方案**不构成超出 Issue 既定边界的新核心架构决策**：持久 cancel_requested 是 Issue 描述明示要求定义的语义且规格推荐；409 契约是规格三的产品决策；血缘复用不改变 schema 语义。判定为「非核心细节方案收敛后直接推进」。Owner 对任一取舍（持久状态形态、409 语义、血缘复用、系统路径不动）有不同判断，在本 Issue 评论否决即暂停对应点返工，规格与方案按决策回写版本。
