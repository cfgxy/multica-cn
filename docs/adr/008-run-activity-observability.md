# ADR 008：Run 活动可观测性与 MCP 查询契约（RUYI-805）

- 日期：2026年10月17日
- 状态：accepted（Owner 在 RUYI-805 决策线程选择 A：持久化语义轨迹为主读取面，worker 终端证据经目标 daemon 受认证代理读取；worker 证据沿用现有 24 小时保留期）
- 范围：只读 MCP `get_run_activity` 的数据来源、响应语义、安全边界与实施验收。不包括完整终端日志长期落库、修改日志采集方式或扩大既有可见性。
- 关联：RUYI-805；ADR-001/002（MCP OAuth 与权限边界）。
- 实施状态：实现已落地于 RUYI-805；真实长时 Run、handler DB 集成及 MCP TypeScript 集成测试仍待独立 QA。

## 1. 背景与目标

RUYI-805 要让有权查看某个 Issue 的协作者判断其某个 Run 是否出现了新的 Agent 活动、是否在等待，或是否已失去 worker 进程。现有 `list_issue_runs` 和 `get_run` 提供平台任务状态、时间、失败信息和重试链，但不读取 Run 的 Agent 消息/工具轨迹；平台 `task_message` 已保存脱敏后的语义活动，受监管 worker 的 stdout/stderr 则只在执行 daemon 的监督目录短期存在。

新增只读 MCP `get_run_activity(workspace, issue, run_id)`，将语义活动、worker 证据和平台状态分账返回。返回值必须使调用者能区分平台状态、进程存活和实际可观察到的活动；`running` 单独不能作为业务进展证据。

## 2. 数据来源与假设

- `task_message` 按 `(task_id, seq)` 保存 Run 消息；写入路径执行敏感信息脱敏，查询可按 `seq > after_seq` 增量读取。数据库既有生命周期不由本 ADR 改变。
- 受监管 worker 的 stdout/stderr 由 supervisor 写入 Run 目录；daemon 从其拥有的 manifest 解析 Run 身份及固定日志位置。收敛且有退出证据的 Run 默认保留 24 小时后由 retention GC 删除。
- worker 日志只经已认证、绑定 runtime 的目标 daemon 代理读取。服务端不能直接访问 daemon 文件系统，也不接收本机路径。
- GC 删除 Run 目录前写入只含 `expires_at` 的 0600 元数据文件；文件名由 task ID 的 SHA-256 派生，不在内容或名称中保存 task/run ID。写入失败则保留 Run 目录，不执行删除。
- 期限元数据从证据到期起最多保留 30 天，由后续 retention GC 清理；损坏或不可判读的元数据保守保留。超过此分类窗口后不再保证区分历史上的 `expired` 与从未受监管的 Run，返回 `not_supervised`。这是有界、非敏感的分类 tombstone，不是日志或业务历史存储。
- `task_message` 的数据库保留边界沿用现有数据库生命周期；本 ADR 不新增或承诺其保留时长。
- worker 活动时间来自 daemon 对日志文件最近写入时间的观察，不冒充 worker 内部事件时间。语义消息时间取持久化 `created_at`；缺失时返回空值，不以查询时间补造。

## 3. 查询、授权与审计

### 3.1 输入

| 字段 | 约束 |
| --- | --- |
| `workspace` | 必需；沿用现有工作区授权解析。 |
| `issue` | 必需；必须属于 workspace。 |
| `run_id` | 必需；精确 Run/Task UUID，必须属于 issue；每次只查询一个 Run。 |
| `after_seq` | 可选非负整数，默认 0；仅返回更大 `seq`。 |
| `semantic_limit` | 可选，默认 50，范围 1–100。 |
| `stream` | `stdout`、`stderr` 或 `both`，默认 `both`。 |
| `tail_bytes` | 每个所选 stream 的请求上限，默认 16384，范围 1–65536。 |
| `worker_cursor` | 可选不透明 cursor，只对同一 daemon、Run、精确 stream 集合有效；省略时从当前尾部读取，不回放完整日志。 |

参数越界或 cursor 与 Run/stream 不匹配时不得静默扩大读取范围。客户端以 `next_after_seq` 和 `next_cursor` 轮询；重试链中的每个 Run 独立查询，不自动跟随或混合游标。

### 3.2 可见性与审计

- 先按既有 MCP 身份、workspace 成员/可见性规则授权，再验证 Issue 和 Run 归属，并执行既有 Run/Agent 可见性检查；不得扩大 `get_run` 可见范围。
- 未授权 workspace 按既有 403 语义处理；未知 Run、其他 Issue 的 Run 或不可揭示的归属差异按既有约定返回不可区分的 404。错误详情不泄漏 daemon 身份或文件路径。
- 每次读取记录调用身份、workspace、issue、run、请求 stream/cursor 范围、结果类别及返回字节数；不记录消息正文、终端原文、凭据或 token。审计写入失败时 fail closed，不返回活动正文。
- MCP 返回前对语义内容及 worker tail 执行服务端二次脱敏和响应限额检查；不得把未脱敏正文写入应用日志或审计详情。

## 4. 响应与诊断语义

- `run_state` 返回平台状态原值、创建/开始/完成时间、耗时与平台已有的明确等待/阻塞/失败原因。状态和耗时不证明 CPU 活跃或业务进展。
- `semantic_activity.messages` 按 `seq` 递增返回现有脱敏消息；含 `next_after_seq`、`has_more` 与基于持久化记录时间的 `last_activity_at`。空消息页与 worker 日志不可用分别表达。
- `worker_evidence` 返回有限 stdout/stderr tail、`next_cursor`、最近观察时间、`process_liveness` 及 `unavailable_reason`。liveness 为 `alive`、`exited` 或 `unknown`；来源冲突或 daemon 不可达时为 `unknown`。日志不可用分类为 `not_supervised`、`expired`、`not_found`、`daemon_unreachable`。
- `progress_assessment` 为 `advancing`、`waiting`、`no_recent_evidence`、`finished` 或 `unknown`，并以 `basis[]` 标出证据来源。只有请求携带前次语义或 worker cursor 且本次发现新序列/字节时才判 `advancing`；首次读取建立基线。明确等待原因判 `waiting`，终态判 `finished`；无新增证据不能称为卡死；关键来源不可用或矛盾时判 `unknown`。进程存活独立返回。
- 没有 supervisor 记录且没有期限 tombstone 时为 `not_supervised`；期限 tombstone 或可信 `ConvergedAt + 24h` 表明超过保留期时为 `expired`；保留期内记录/日志缺失为 `not_found`；目标 daemon 不可达、请求超时或关联失败为 `daemon_unreachable`。
- Worker cursor 由 daemon 进程随机密钥签名，并绑定 daemon、Run、精确 stream 集合和签发时间；篡改、错配、重启后密钥变化或超过 24 小时均拒绝。无效/过期 cursor 不降级为完整 tail。

## 5. 增量、大小与频率限制

- 语义读取以单调 `seq` 分页，最多 100 条；单条消息序列化后最多 8 KiB，截断须标记。
- worker 每个所选 stream 最多 64 KiB，同时读取两个 stream 时总响应仍受 160 KiB 硬上限约束（含语义内容和 JSON 结构）。
- 同一调用身份和 Run 每滚动 10 秒最多读取一次；限流响应给出可重试提示且不推进 cursor。
- cursor 只能推进到本次成功交付的最后一个完整事件/字节位置；超限时未交付内容留待后续读取。
- 服务端只传输增量语义记录和有限 tail，不重复发送完整历史或终端文件。

## 6. 不可用边界

| 场景 | 响应 |
| --- | --- |
| Run 无语义消息 | `messages=[]`、游标有效、`last_activity_at=null`；不推断 worker 日志为空。 |
| 未受 supervisor 监管 | `not_supervised`；liveness 为 `unknown`，除非另有权威现存证据。 |
| worker 证据超过 24 小时保留期 | 30 天 tombstone 分类窗口内为 `expired`；超出分类窗口后不再保证历史区分，按 `not_supervised` 处理。 |
| 记录/日志在保留期内缺失 | `not_found`，不能伪装为空 tail。 |
| daemon 不可达 | `daemon_unreachable`；平台状态与语义活动仍可独立返回，liveness 为 `unknown`。 |
| 无权限/跨 workspace/issue | 既有 403/404 语义，不返回活动正文或存在性侧信道。 |
| Run 已结束 | 返回平台终态及可用退出证据；worker tail 仍受 24 小时保留期限制。 |
| cursor 过期或不匹配 | 明确拒绝；授权通过后调用者可从最新 tail 重新建立基线，不自动回放完整日志。 |

## 7. 实现映射与回退

| 契约 | 实现位置 | 当前证据 / QA 状态 |
| --- | --- | --- |
| MCP 输入、工具说明、响应类型 | `apps/mcp/src/tools.ts`、`apps/mcp/src/types.ts`、`apps/mcp/README.md` | MCP 定向 Vitest/TypeScript 待独立 QA。 |
| workspace/issue/run 授权、语义分页、审计、脱敏、限流、进度 | `server/internal/handler/run_activity.go` 与 `run_activity_test.go` | handler DB 集成待独立 QA；开发测试曾受共享库 migration ledger guard 阻断。 |
| 认证 daemon 定向请求/响应与 runtime 关联 | `server/internal/daemonws/hub.go`、`server/internal/daemon/wsrpc.go`、`server/pkg/protocol/*` | daemonws 定向测试通过；真实端到端轮询待 QA。 |
| manifest 所有权检查、固定日志读取、签名 cursor、liveness | `server/internal/daemon/supervisor/evidence.go`、`evidence_test.go` | supervisor/daemon 定向测试通过；真实 systemd 状态变化待 QA。 |
| 24 小时证据 GC、期限 tombstone 与 30 天清理 | `server/internal/daemon/supervisor/retention.go`、`manifest.go`、`retention_test.go` | retention 定向测试覆盖删除、保留和 tombstone 清理；真实长时 Run 留待 QA。 |

daemon 代理不可用时仍可返回平台状态和语义轨迹，worker 证据明确为 `daemon_unreachable`；禁止 server 直读 supervisor 目录。权限核验、审计或二次脱敏失败时拒绝返回活动正文。增加终端长期持久化、改变 24 小时 worker 日志保留期或扩大跨 Issue 查询均超出本 ADR，需另行决策。

## 8. 验收矩阵

| 验收场景 | 开发侧覆盖 | 独立 QA / 限制 |
| --- | --- | --- |
| 语义增量、首次读取基线、进度分类 | `run_activity_test.go` 定向单测 | DB 集成与真实 MCP 调用待 QA。 |
| 新 stdout/stderr、cursor 连续读取 | `evidence_test.go`、`rpc_dispatch_test.go`、daemonws 定向测试 | 真实长时 Run 持续写入和端到端 MCP 轮询待 QA。 |
| 明确 waiting、running 无新增证据、finished | handler 进度定向测试 | 以真实长任务状态复核待 QA。 |
| alive/exited/unknown 与 manifest 所有权 | supervisor/daemon 定向测试 | systemd/daemon 实际退出与重启待 QA。 |
| 未监管、过期、缺日志、daemon 不可达 | evidence 分类测试、Hub 关联测试 | 真实断连与环境边界待 QA。 |
| 跨 Run/stream、篡改、重启及过期 cursor | 签名 cursor 定向测试 | 已由开发侧自动化覆盖；QA 可复核接口响应。 |
| 权限隔离、审计 fail-closed、脱敏、限流/响应上限 | handler 定向测试 | handler DB 集成与性能/权限 QA 待执行。 |
| retention GC 与元数据生命周期 | supervisor retention 测试覆盖 GC、失败保留及 tombstone 清理 | 长期运行累积行为按固定 TTL 静态/定向断言；不做资源压测。 |
| MCP 工具 schema 和调用输出 | 已实现工具及 README 映射 | MCP TypeScript 测试因本地缺少 `node_modules` 未运行，交独立 QA。 |

## 9. 阶段记录

- 决策：Owner 选择方案 A；本 ADR 是正式契约，README 仅作工具使用说明。
- 实现：RUYI-805 完成开发侧实现；完整代码 delta 基于 `origin/main`，文档与期限元数据生命周期说明纳入同一交付。
- 未完成验收：真实长时 Run 的持续 stdout/stderr 增量、真实 daemon/systemd 存活变化、端到端 MCP 轮询、handler DB 集成与 MCP TypeScript 测试均留给独立 QA。
- 数据边界：不恢复此前已删除且无期限元数据的历史日志；不持久化终端原文；期限 tombstone 最长在过期后保留 30 天。
