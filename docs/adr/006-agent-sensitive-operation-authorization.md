# ADR 006：Agent 敏感操作白名单授权通道——决策请求一等实体与平台代执行（RUYI-630）

- 日期：2026年10月10日
- 状态：accepted（Owner 五项决策卡裁定：决策1=A 授权面与平台执行器一体交付；决策2=B 只读+低风险写入，提示词恢复为首个白名单写操作，账单/支付类永不登记；决策3=B 跨空间时目标空间 Owner 二次确认强制；决策4=B 默认 24h 过期、发起方可声明更短、平台封顶；决策5=方案甲 按空间一行、目标行可操作、发起行只读投影、同事务双写、授权摘要详情不进 Issue 页）
- 范围：`server/migrations/940_decision_requests.*`、`server/pkg/db/queries/decision_request.sql`、`server/internal/handler/decision_request.go` / `decision_request_execute.go` / `issue_decision.go`（授权卡分支）/ `decision_text_answer.go` / `issue_decision_batch.go`、`server/cmd/server/router.go`（路由挂载）、`server/internal/handler/workspace_delete.sql`（级联）、`packages/core`（类型/zod schema/api client/query hooks/WS 事件）、`packages/views`（决策中心授权区、信息流授权卡、四语言 locale）、`apps/mobile`（决策 tab 授权区、信息流授权卡）
- 关联：MUL-2600（决策卡 answer_source 结构化来源）、MUL-3292（决策卡人审闸门）；926 审计新增 `trigger_kind=decision_request`
- 实施状态：已实现（服务端测试、三端 typecheck 与单测通过）；QA 验证与槽位迁移冒烟未执行，见 §9

## 1. 背景与问题

Agent 在任务执行中会遇到需要人类授权的敏感操作：读取另一个工作区的基本信息、回滚最近一次提示词应用等。现状的决策卡（RUYI-345）是绑定 Issue 的问题卡——语义是「结构化提问」，没有操作执行语义，也无法表达「跨空间需要两个空间各自确认」的授权链。若不给通道，Agent 要么放弃操作，要么绕过人审（不可接受）；若滥发凭据，则把提权面直接交给机器身份。

目标：给 Agent 一条**白名单授权通道**——Agent 只能发起、不能自答；人类在结构化载体上作答；平台在两步授权齐全后代为执行；账单/支付类操作永远不进入白名单（human-only 闸门不放宽）。

## 2. 决策请求实体（方案甲载体）

新增 `decision_requests` 表：**每个涉入空间一行**，由 `request_group_id` 分组。行的角色二分：

- `target` 行：目标空间，`operable=true`，操作者档位在创建时强制为 `owner`（决策 3B 的二次确认；不可降档）。
- `origin` 行：发起空间，默认 `operable=false` 只读投影。例外：请求不携带 Issue 引用时，origin 行自身可操作（无 Issue 场景兼容条款）。

关键不变式：

- **组级状态单事务双写**：任一行的答备/终态转换在同一事务内传播到全组行（`FOR UPDATE` 组锁 + 传播循环），任何空间看到的授权状态一致。
- **摘要与 Issue 内容隔离**：行上只留 `origin_issue_id` + `origin_issue_title` 标题级引用；决策中心列表/详情端点不返回 Issue 正文、评论。授权摘要永不进入 Issue 页面（决策 5），Issue 侧只有一个授权链接卡。
- **发起方与撤销**：`created_by_type` 恒为 `agent`；发起 agent 可撤销自己的 pending 请求，工作区 Owner 亦可；撤销后组级 `revoked`。

## 3. 授权面与操作者档位

- 创建：`POST /api/workspaces/{id}/decision-requests`（agent 经任务令牌发起）。`action_type` 必须命中白名单注册表；`risk_tier` 由注册表派生（服务端权威），客户端声明无效。同空间请求生成单行；跨空间请求生成 origin+target 两行（同事务）。
- 答备：`POST /api/workspaces/{id}/decision-requests/{requestId}/answer`，路由层 `RequireHumanActor` + handler 层成员身份与档位复核双闸——机器凭据即使误达路由也被拒。档位三档：`owner`（默认，fail-closed）/ `named`（Owner + 指定人）/ `members`；跨空间 target 行锁 `owner`。
- 过期（决策 4B）：发起方可声明 `ttl_minutes`（更短）；平台默认 24h、上限 24h、下限 5m。惰性过期：读路径与列表路径触发清扫，过期组回调发起 agent 一次（`terminal_callback_at` 保证至多一次）。
- 取消：`POST .../cancel`，发起 agent 或工作区 Owner；返回 `{"status":"revoked"}`。

## 4. 白名单注册表与平台代执行

注册表（`decisionRequestActions`，handler 内闭合集合）：

| action_type | risk_tier | 语义 |
| --- | --- | --- |
| `workspace_info_read` | `read` | 读取目标工作区基本档案（名称、描述）——该空间任何成员本可见的信息 |
| `prompt_restore` | `write_low` | 撤销 agent/squad 上最近一次市场提示词应用（一步恢复，手改守卫） |

账单/支付类**永不登记**（决策 2B）：注册表是唯一门，未注册的 action_type 在创建即 400，不存在运行期扩权。

执行机制（决策 1A 的执行器半边）：`advanceDecisionRequestAfterAnswer` 是全组唯一推进漏斗——组锁串行化竞争终步 → 全组 approved 时在同事务内调用 `executeDecisionRequestAction` → 组级行 `executed/execute_failed` 回写（`SetDecisionRequestExecution` + 卡面 `SetAuthorizationCardExecution`/`SyncDecisionCardsForGroup`）→ 926 审计。

**内部执行上下文**（安全核心）：执行器复用人端点相同的 sqlc 查询与守卫（如 restore 的 `lockPromptTarget` FOR UPDATE 锁、手改 sha256 守卫、目标工作区包含性检查），但**不签发任何 mat_ 令牌、不经过 RequireHumanActor 面、无 HTTP surface**——授权唯一来源是这条已批准的请求行。Agent 不获得任何可复用凭据；每次执行都必须有当次的人类授权。

审计两步走（926）：`agent.decision_requested`（创建）与 `agent.decision_authorized`（人类同意，含 answer_source）+ 执行终态事件，`trigger_kind='decision_request'`、`trigger_ref=request_group_id`，一条查询可重建整条授权+执行链。

## 5. 来源标记与答案通道

`answer_source` 三通道枚举（`card_click` / `text_token` / `batch`），全部由服务端在写入时判定，**禁止解析用户文本推断来源**（MUL-2600 对齐）。授权链接卡（issue_decisions 上 `decision_kind='authorization'` 的卡）只有按钮点击一条路：`AnswerAuthorizationDecisionCard` 固定写 `card_click`，CAS 防双击，且在同一事务内同步 origin 投影行（卡与行一个真相；行已终态则回滚卡答备）。步骤回声评论不带 mention——发起 run 只在终态回调唤醒一次。

既有问题卡面同步升级：三通道 answer_source 写入、`visible_tier` 可见档位过滤（members/restricted，机器身份不可见 restricted 卡）、`operator_tier`+`named_approver_ids` 档位闸、自定义双按钮标签。问题卡三态语义（open/answered/cancelled）不变，存量行回填 `decision_kind='question'` 默认值，零破坏。

## 6. 三端载体

- **web/desktop**（共用 packages/views）：决策中心新增「授权请求」独立区（`DecisionRequestRow`：动作/风险/状态徽标、发起者、来源 Issue 标题、可操作行持同意/拒绝/撤销按钮、投影行提示、失败原因；跨空间步骤展开走详情端点）；该区不进入问题卡的列表/看板分桶。信息流授权卡分支：双固定按钮、无自由选择、无取消按钮（生命周期在决策中心），auth_state/执行结果徽标。
- **mobile**：决策 tab 顶部授权请求区 + 信息流授权卡（RN 移植，同语义）；mobile 自有 api wrapper 与 query options，缓存 key 复用 core 的 `decisionRequestKeys`（纯数据）保证三端 WS 失效同址。
- WS：新增 `decision_request:updated` 事件（载荷 `{request, request_group_id}`），客户端失效 `decisionRequestKeys` 子树；卡面变化走既有 `decision:updated`。
- i18n：四语言（zh-Hans/en/ja/ko）`decisions:requests.*` 与 `issues:decisions.auth_*` 全量键，parity 测试与 mobile 前缀锁测试覆盖。

## 7. 被否方案

- **复用问题卡**：问题卡是 Issue 绑定的提问语义，无执行语义、无跨空间两步链、无组级状态传播——把授权语义塞进问题卡会让两个载体都变脆。
- **给 Agent 签发短期提权令牌**：凭据可复用、可泄漏，授权粒度从「单操作」退化到「时间段」；违反「不向 Agent 签发提权凭据」红线。
- **仅 Issue 行（不按空间分行）**：目标空间 Owner 在 Issue 信息流里确认，等于把目标空间的授权决策绑进发起空间的 Issue 页——可见性档位与空间成员资格对不齐，授权摘要进 Issue 页也违反决策 5。
- **异步队列无一等实体**：无状态行则无组级一致性、无惰性过期、无幂等终态回调，审计也只能靠日志拼。

## 8. 后果与兼容性

- schema：940 单 stem（单 Issue 单编号），两张改动一体交付；无外键（仓库红线），`DeleteWorkspaceLeafData` 显式级联清理 decision_requests 行与组内授权卡。
- 问题卡零破坏：全部新列 nullable/带默认，既有查询语义不变；`auth_state` 对问题卡恒 NULL。
- 路由：授权面挂在既有 workspace 成员中间件下，读成员可见；answer 路由额外 `RequireHumanActor`。
- 成本：每请求一行/空间，授权是低频事件，无容量顾虑；决策中心列表沿用 bounded window + 服务端 counts。

## 9. 验证标准与当前状态

- 服务端：`TestDecisionRequest*` 九组测试覆盖规格第 6 节第 1 列九条 + 方案甲专项（跨空间双行双写、拒绝传播、三通道来源、惰性过期、创建/Owner 撤销、restore 执行三态、审计链、agent 轮询、工作区级联），隔离库 `multica_ruyi630_test` 全绿；`env -u MULTICA_*` 执行。
- 三端：`@multica/core`、`@multica/views`、`@multica/mobile` typecheck 通过；views 决策中心/授权卡测试、parity 测试（194）、core 全量（2111）、mobile vitest（1325）+ jest（267）全绿。
- 未完成（QA 阶段）：槽位迁移冒烟（dev1/dev2 槽位在 qa 相位占用中，以隔离库代跑）、`make check` 全量、真实三端运行验证。
