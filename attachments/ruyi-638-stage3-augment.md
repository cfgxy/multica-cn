# RUYI-638 验收标准裁决增补（v3 增补件）

- 交付人：谢小婷（PM）｜2026-10-10｜触发：Owner 决策 1 选 B、决策 2 选 C（2026-10-10 15:52:30）
- 本文件为 `ruyi-638-stage2-acceptance.md` 的裁决增补件：替代其中 W5、K4、M3 三条「未决即排除」占位行，其余 18 条与总纲各条不追溯重写。
- 口径与 v2 一致：用户视角判定句、含明确通过/失败判据、第三方可机械复核、只写「验什么」不写「怎么实现」；基线口径 =「对照桌面/Web 同功能行为一致」；标注【QA 实机】/【静态验收】/【实机抽查】。
- 事实锚点（只读核对主检出，基线 SHA `80a224248cd7918cc7120cb0f5143eacf125fe65`，仅作静态判据依据）：web integrations = settings 内 composio（toolkit 目录 + 连接，connect 走 OAuth 回调、disconnect 带确认）与 lark（已接入 bot 列表，断开仅 admin）；billing = `/billing` 页 + Stripe Checkout/Billing Portal 跳转；marketplace = prompt market 面两段式（market/installed，安装动作）；admin = users/workspaces/mcp/oauth clients 四 tab，users 面高危写为禁用/超管授予撤销（含原因）/impersonate（含原因与确认框），审计落点 `admin_audit_log`，动作枚举 `user.disable` / `super_admin.grant` / `super_admin.revoke` / `impersonation.start` / `impersonation.stop`。

## 一、分期阶段 3 后半增补：integrations 移动端只读列表（W5 组，替代原 W5 占位行）

定稿口径：移动端展示「已接入集成」只读列表（名称/状态/账户信息）；接入（connect/OAuth）、断开（disconnect）、重配置等写流留 web，移动端设引导入口。

- W5a【QA 实机·双端对照】同账号同 workspace 下，手机端展示的已接入集成集合与连接状态和 web settings integrations 面一致（counts parity）；移动端列表口径为「已接入集成」，不混入 web 目录中未接入的可连接项。失败判据：任一集成缺失/多出、状态不一致，或移动端出现未接入目录项。
- W5b【QA 实机】列表项可读出集成名称、连接状态与账户标识信息，字段语义与 web 列表同义。失败判据：无法据列表识别对应集成或账户，或字段口径与 web 不同。
- W5c【QA 实机·负向】移动端集成列表无任何写入口：接入、断开、重配置均不可触发（不可触发，而非仅隐藏）。失败判据：任一写动作在移动端可触发，或以可用控件形式存在。
- W5d【QA 实机】集成页提供前往 web 配置的引导入口，入口可达、落点为 web 端对应 integrations 配置面。失败判据：无引导入口、落点 404/报错、或落点非该集成配置面。
- W5e【QA 实机】可见性一致：凡 web 端可见 integrations 面的账号，手机端同账号可达只读列表；web 端不可见者手机端同配置不可达。失败判据：任一账号双端可见性不一致。
- W5f【QA 实机】无已接入集成时展示空态视图与 web 配置引导，不报错、不永久 loading。失败判据：空列表下报错、死 loading、或出现不可用的写控件。

## 二、分期阶段 5 增补：billing / marketplace web-only 引导入口（K4 组，替代原 K4 占位行）

定稿口径：billing 与 marketplace 一期 web-only，移动端仅设引导入口（与统计冷启动「前往桌面端查看完整统计」同一产品语言）；integrations 只读列表已归阶段 3 后半（W5 组），自本阶段移出。

- K4a【QA 实机·负向】手机端不存在 billing 订阅/支付与 marketplace 浏览/安装的可操作界面，两类功能仅以引导入口呈现。失败判据：手机端出现任一可触发交易、订阅变更或安装的控件，或出现半成品页面。
- K4b【QA 实机】billing 引导入口出现在与 web 功能对应的移动端入口位，点击后落点为 web 端 billing 面起点（订阅/Portal 跳转起点），全程无 404/报错。失败判据：入口缺失、落点错误或中途报错。
- K4c【QA 实机】marketplace 引导入口同 K4b：落点为 web 端 marketplace 面。失败判据：同 K4b。
- K4d【静态验收】web-only 边界静态核对：移动端代码无 billing 结账/Portal 与 marketplace 安装类 API 调用锚点。失败判据：存在上述任一调用锚点。

## 三、分期阶段 6 增补：admin 全量移动端化（M3 组，替代原 M3 占位行；原「全部写操作留桌面端」口径作废）

定稿口径（Owner 决策 2 选 C，Owner 指令，风险由 Owner 承担）：admin 四面（users/workspaces/mcp/oauth clients）全部移动端化，含 impersonate 与超管授予等高危写。缓解不收范围，以验收承载三层：二次确认强度（M3b）、审计对齐（M3c）、负向断言（M3f）。M1（只读列表/审计查看）、M2（非超管不可达）不变，继续有效。

- M3a【QA 实机】写动作覆盖面：web admin 现网全部写动作在手机端均可达且可完成——users：禁用/解禁、超管授予/撤销、impersonate 发起/停止；oauth clients：创建/编辑/密钥轮换/删除/授权撤销；mcp server 与 workspaces 面以 web 现状写动作为清单基线逐一可达。失败判据：任一 web 写动作在手机端缺失或不可完成。
- M3b【QA 实机·高危】二次确认强度：impersonate、超管授予/撤销、用户禁用、oauth client 删除/授权撤销/密钥轮换在手机端必须二次确认，确认信息与 web 同语义——对象标识 + 动作后果说明，凡 web 要求原因输入者手机端同样要求且不可跳过。失败判据：任一动作无确认流、确认内容弱于 web、或原因输入可跳过。
- M3c【静态验收 + 实机抽查】同 API 同审计落点：全部写动作与 web 命中相同服务端端点，审计落点同为 `admin_audit_log` 且动作枚举一致（users 面为 `user.disable` / `super_admin.grant` / `super_admin.revoke` / `impersonation.start` / `impersonation.stop`，oauth/mcp 面以 web 现状为基线）。实机抽查：手机端执行一次用户禁用与一次超管授予，web admin 审计记录出现对应条目且 actor/target/action 与操作一致。失败判据：审计缺条目，或 actor/target/action 任一字段不符。
- M3d【QA 实机】impersonation 会话标识：手机端发起 impersonate 后全程有醒目 impersonation 标识（与 web banner 同语义）；停止后回到超管自身身份，无残留目标身份权限。失败判据：无标识、或停止后身份/权限残留。
- M3e【QA 实机】双端状态一致：任一写动作完成后，web admin 同步反映新状态（同数据身份、同 WS 事件覆盖）；反向（web 侧变更）手机端刷新后一致。失败判据：任一动作后双端状态不一致。
- M3f【QA 实机·负向】非超管不可达不放松：admin 四面的读与写对非超管账号在手机端完全不可达（不可触发，而非仅隐藏），直接构造入口/深链同样不可达。失败判据：非超管可达任一 admin 读面或触发任一写动作。
- M3g【QA 实机】密钥一次性展示：oauth client 创建/轮换产生的 secret 仅一次完整显示，此后不可再查看，语义与 web 一致。失败判据：secret 可重复查看或明文常驻界面。

## 四、总纲同步（定稿口径）

- 原三条「标注·未决即排除」占位行（W5/K4/M3）由本次增补的 W5a–W5f、K4a–K4c（+K4d）、M3a–M3g 替代；其余条目不追溯重写。
- 决策 1 定稿（Owner 选 B）：integrations 移动端只读列表 → 阶段 3 后半（W5 组）；billing 与 marketplace web-only + 引导入口 → 阶段 5（K4 组）。
- 决策 2 定稿（Owner 选 C）：admin 全量移动端化含高危写 → 阶段 6（M3 组）；与 Leader 原推荐（高危写留桌面）不同，Owner 明示选择，风险留痕见 Leader 2026-10-10 收口评论。
- 增补条目已逐条标注【QA 实机】/【静态验收】/【实机抽查】并含通过/失败判据，与既有 18 条口径一致。
- 每阶段通用验收（counts/permissions/state enums/data identity 四项 parity、WS 覆盖、负向断言）对增补条目同等适用。
