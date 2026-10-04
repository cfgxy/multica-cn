# ADR 003：统一审计事件（audit_event）

- 日期：2026年10月04日
- 状态：accepted（RUYI-355 Phase 1；Owner 于 2026年10月03日 批准 P1 实施，评论 `01a10466`）
- 范围：服务端五域运营事件（issue / run / agent / runtime / ops）的统一写入契约、查询面（REST + MCP）与 Phase 1 覆盖矩阵落地。

## 背景

「谁在什么时候对哪个对象做了什么、为什么」目前散落在三处互不相通的结构里：`activity_log`（issue 时间线，域窄、无运行语义）、`agent_task_queue` 的取消归因列（只有终态快照，历史查询要拼六张表）和结构化日志（不可查询、不面向成员）。三处词汇互不兼容：同一次「用户取消了一个 run」在时间线里是 actor+action、在队列表里是 cancel_requested_by_user_id + failure_reason、在日志里只有一行 WARN。RUYI-292 补取消归因时已经出现第三次为「可查询的因果」再造轮子。

## 决策

新增 append-only 表 `audit_event`（迁移 `922_activity_audit`，单 stem：表 + 四对象维度部分索引 + `agent_task_queue` 归因三列）。所有跨域运营事件经 `server/internal/service/audit.go` 的唯一契约写入：`Event` + `Validate()` 是必填字段的唯一执法点（workspace/domain/event_type/occurred_at/actor_type 必填，actor_id 除 system 外必填，event_type 必须 `<domain>.<action>` 前缀，run 事件必带 task_id、runtime 事件必带 runtime_id、issue 事件必带 issue_id、agent 事件必带 agent_id，cancel/fail 类必带结构化 reason）。

写入双通道按语义划分：`AppendAuditEvents` 在调用方事务内批量插入，失败即回滚——用于取消归因、fail-closed 的 env 审计等「审计行是业务保证一部分」的路径；`TryAppendAuditEvents` 事务外 best-effort，失败只打 WARN——用于生命周期、清扫器裁决、daemon 注册等「事件是旁路记录」的路径。因果链不建父指针，由共享对象维度（issue_id/task_id/agent_id/runtime_id）+ trigger_kind/trigger_ref + occurred_at 排序重建。actor 归因约定：member 必带 id；system 无 id；daemon 用 runtime id；agent 用消费方任务 id。

取消归因下沉到 SQL：18 条 Cancel*/Fail* 查询的 SET 子句直接写 `cancel_reason/cancel_actor_type/cancel_actor_id` 三列，与状态翻转同语句生效；RUYI-292 两阶段取消矩阵在 daemon ack 确认终态（`ConvergeCancelRequestedToCancelled`）时继承受理人归因。同事务紧跟着为每个被取消行写 `run.cancelled` 审计事件，6 条服务端批量取消路径（issue 删除/取消、agent 停用/归档、触发评论删除、chat 会话删除、runtime 清扫、任务合并消费）与用户单任务取消全部对齐同一 reason 词表。

查询面单一：`ListAuditEvents` 一条 sqlc 查询（时间窗 + 域/类型/actor/对象维度/reason 过滤 + (occurred_at, id) keyset 游标），workspace 级 `GET /api/workspaces/{id}/audit-events` 与 issue 级 `GET /api/issues/{id}/audit-events`（issue_id 钉死）共用；MCP `search_audit_events` 工具走同一 REST 端点——三个面一个实现，过滤语义修正一次生效。issue 域 Phase 1 与 `activity_log` 双写（事件总线监听器镜像 10 类字段变更 + 5 个直写点补 audit 孪生），Phase 2 再切换时间线数据源后收敛。

Phase 1 覆盖矩阵：run 全生命周期（queued/dispatched/started/waiting_local_directory/cancel_requested/completed/failed/cancelled/retried/rerun）、runtime 连接与清扫（daemon 注册→connected、主动注销→disconnected、心跳超时→offline_detected、GC→runtime.gc、重连宽限耗尽→reconnect_exhausted）、agent 安全事件（env_revealed/env_updated 与活动记录同闸门 fail-closed、execution_profile_activated、runs_cancelled）、运维锚点（`server.started` 规范化为 `ops.server_started`——CHECK 约束要求事件类型必须域前缀，启动时按 workspace 各写一条、details 带 version/commit）。

## 安全语义

`audit_event` 载荷禁止记录环境变量明文值与任何凭据：env 类事件只记 key 名、数量与操作者（沿用 activity_log 既有脱敏口径）；run.failed 的原始错误文本留在任务行，审计只带结构化 reason 与受控 details。env 读写两个入口的审计孪生与明文返回同闸门：`agent.env_revealed` 任一记录写失败即拒绝返回明文，`agent.env_updated` 孪生与更新同事务提交或回滚。

## 权衡与回退

append-only 无外键是有意的：审计行必须比被描述的业务行活得久，workspace 删除清理由应用层显式处理（沿仓内「禁数据库外键」红线）。双写期 `activity_log` 仍是时间线事实源，audit 镜像行丢失只留 WARN 不阻塞业务（env 两处除外，其 fail-closed 语义与既有行为一致）。事件词表开放不锁枚举：新事件类型无需迁移，但必须走 audit.go 契约。回退路径：停用写入点即停止增长；表与索引可整体 drop，不影响任何业务读路径；922 迁移可 down 干净回滚。

验证以 `server/internal/service/audit_test.go` 契约单测、`go build ./...` + `go vet` 全绿、sqlc 生成物零 diff、迁移 up/down/up 冒烟为准；实机多端渲染与查询联调由 QA 阶段完成。
