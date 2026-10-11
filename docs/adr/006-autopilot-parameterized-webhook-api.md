# 6. 自动化通用 Webhook 与 autopilot 参数化 API

- 日期：2026-10-10
- 状态：已采纳（Owner 决策卡批复，RUYI-641）
- 关联：RUYI-641；退役对象为 RUYI-52 引入的 agent_webhook

### 背景

智能体专属 Webhook（`agent_webhook` 表 + `/api/webhooks/agents/{token}`，固定 prompt、访问即建对话 session、token 前缀 `awt_`）与自动化任务（autopilot）的触发能力重叠，生产库存量 0 行。autopilot 侧已有通用 Webhook 触发（`/api/webhooks/autopilots/{token}`，provider=generic/github，投递去重与审计 `webhook_delivery`）与手动触发 API（`POST /api/autopilots/{id}/trigger`，Bearer 鉴权已兼容 PAT、支持 Idempotency-Key），但请求体不支持传参，模板插值仅 `{{date}}`。

### 驱动因素

- 双套触发/凭据/审计/管理面并行，维护成本高且能力重叠。
- 固定 prompt 无法承接变量输入，自动化无法作为可复用 API 对外。
- 生产 0 行，退役成本极低。

### 备选方案与决策（均经 Owner 批复）

1. 实施编排：三阶段同单顺序交付（采纳）/ 退役拆后续单 / 先退役再参数化。理由：A/B 共用参数校验与快照机制，顺序交付避免返工；C 无存量影响放最后，避开 RUYI-605 在途 mobile 改动。
2. API 鉴权：PAT 单通道（采纳）/ 复活 `autopilot_trigger` 的 `api` kind 双通道。理由：`/trigger` 已兼容 PAT，零新增凭据管理面；`api` kind 为历史占位（创建即 400），复活即双 token 体系。本决定不删除该占位（手术式演进）。
3. generic webhook 可选 HMAC：本单不做（采纳）/ 本单一并做。理由：URL token 已是完整 bearer 凭据；签名属增量加固（防重放/令牌泄露放大），不在本单验收关键路径。

### 后果

- 新表 `autopilot_param`（参数名/类型/描述/必填/默认值/枚举选项，隶属 autopilot），类型五类：string/text/number/boolean/enum。
- 任务指令与 issue 标题模板支持 `{{参数名}}` 插值；`{{date}}` 保留；未定义 `{{...}}` token 维持 create/update 时拒绝（在现有校验上扩展参数白名单）。
- 触发语义：schedule 用默认值；webhook（generic/github）/手动/API 可传 params；必填缺失或类型不符拒绝并记录原因；参数值快照落 `autopilot_run.trigger_payload` 供审计回放。
- 每个 autopilot 即参数化 API：参数定义经详情接口暴露为调用契约，归因 PAT 属主。
- `agent_webhook` 全量退役：server、packages/core、packages/views、apps/mobile、docs 全仓零残留；迁移 `942_drop_agent_webhook` 删表（migration 900 建，生产 0 行）。
- 语义差异：agent_webhook 触发对话 session，autopilot 触发任务队列；存量 0 行，无实际用户影响。

### 验收标准

- 阶段 A：参数定义 CRUD 与校验、模板插值、触发期参数解析与拒绝分支、快照持久化单测先红后绿；sqlc 生成物 diff 干净；941 槽位库冒烟。
- 阶段 B：`/trigger` params 契约、详情接口暴露参数定义、PAT 属主归因断言测试。
- 阶段 C：`git grep -i agent_webhook` 全仓零命中（含 docs 与脚本）；942 冒烟；workspace 删除 manifest 同步更新且测试覆盖；web/mobile 构建通过。
- 整体：全仓测试绿；三阶段 commit 边界可辨。

### 回退路径

- 阶段 A/B 为纯增量，revert 提交即回退；941 仅落过非生产库。
- 阶段 C：942 上生产后表数据不可恢复，但生产 0 行；回退 = 旧版本部署 + 重放 900 建空表；外部持有 token URL 失效面 = 同一 0 用户集。
