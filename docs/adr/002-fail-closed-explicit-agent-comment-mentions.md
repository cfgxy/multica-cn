# ADR 002：评论中的显式智能体提及反馈

- 日期：2026年09月28日
- 修订：2026年11月05日
- 状态：superseded（发送前硬拒绝已由逐目标软 admission 取代）
- 范围：工作区内普通任务评论的 POST、正文发生变化的 PUT，以及同一权限口径的触发预览。

## 背景

显式智能体提及同时承载两件事：评论正文记录协作事实，触发系统尝试启动目标智能体。目标 UUID 写错、目标归档或调用权限不足时，触发不应发生；但把这类协作语义错误提升为整条评论的写入门闸，会丢失合法汇报并诱发调用方重复改写或重试。

## 当前决策

评论内容与触发结果分开处理：

- 通过身份、作者、revision、task/no-action 等写入边界后，评论可以保存。
- 每个可解析的显式 `@agent` / `@squad` 目标在成功响应的 `trigger_outcomes` 中返回 `queued`、`coalesced`、`deferred` 或 `blocked`。
- 未知或跨工作区目标与不可调用的私有目标统一返回 `blocked/invocation_not_allowed`，不泄露目标是否存在。
- 已归档或格式可解析但无法定位的目标返回 `blocked/target_unavailable`，且不创建 Run。
- 完整但不符合 mention parser 语法的 agent 链接通过 `invalid_mentions` 返回 UTF-8 字节半开区间 warning；评论仍保存。
- 同一 task 在同一任务中提及自身时返回 `blocked/self_trigger_suppressed`；跨任务的合法自交接不受此规则扩大限制。
- 智能体评论不强制「必须且最多一个责任 mention」，Squad worker 也不由平台代码强制提及 Leader。多个显式目标逐一返回 outcome，实际执行继续按目标智能体原子去重。

预览与保存使用同一触发解析：预览同时返回可运行目标、blocked outcome 和 unparsed-link warning。`/note` 仍不触发智能体，也不产生 mention warning。

## 保留边界

软化只作用于评论内容的协作语义，不放宽目标 dispatch 权限、工作区隔离、归档与运行时可用性检查、同任务 self-trigger suppression、agent-only cascade 熔断、task role conflict、原子去重、no-action 互斥、作者权限和 revision 校验。

## 兼容性

`trigger_outcomes` 与 `invalid_mentions` 都是成功响应上的附加字段，旧客户端可以忽略。CLI table 输出会提示「评论已保存但目标未触发」或无法解析的链接区间；JSON 调用方可以直接读取结构化结果。客户端仍可识别旧服务端返回的 `invalid_agent_mentions` 422，以兼容版本错配，但新服务端不再产生该语义拒绝。

## 验证

`server/internal/handler/comment_mention_admission_test.go` 覆盖 create/edit 对称性、非法目标、多个目标、智能体自 mention、普通终态汇报和 Squad worker 交接；`comment_trigger_outcomes_test.go`、`mention_self_trigger_test.go`、`comment_duplicate_enqueue_race_test.go`、`task_no_action_test.go` 继续验证保留边界。CLI、MCP 和 core schema 测试验证 warning 与 outcome 的向后兼容解析。
