# RUYI-638 阶段 1：手机端对照 Desktop 能力差距盘点报告

- 基线：`cfgxy/multica-cn` @ `80a224248cd7918cc7120cb0f5143eacf125fe65`（2026-10-09 22:43:31 +0800，与主检出一致）
- 盘点方式：只读静态盘点，零代码改动。全文证据均为「实测（静态代码锚点）」——指代码文件、路由挂载、import 关系可由第三方按 `文件:行号` 机械复核；**运行时行为（真机渲染、接口实调）未实测**，如涉及以「推定」标注。
- 在飞协调：RUYI-637（开放 PR #313，`feat(mobile): add "edit in full form" recovery entry for quick-create outcomes (RUYI-605)`）改动面为 `apps/mobile` 的 `new-issue.tsx`、`inbox/[id].tsx`、`components/issue/manual-create-panel.tsx`、`data/stores/new-issue-prefill-store.ts`、`lib/quick-create-edit.ts`、`lib/use-mention-input.ts`——与本报告五域及缺失面**零交集**；后续分期实现只需继续避开 issue 快建/编辑交互面即可。

## 1. 三端技术形态（补齐路径的硬边界）

| 端 | 形态 | 业务页面来源 | 逻辑层 |
| --- | --- | --- | --- |
| Desktop | Electron 壳 + 自建 React SPA（react-router memory router） | 直接复用 `@multica/views` 页面组件 | `@multica/core` + 桌面专属 daemon/更新/Tab 系统 |
| Web | Next.js App Router | `apps/web/app/**/page.tsx` 全部为 `@multica/views` 薄转发（如 `apps/web/app/[workspaceSlug]/(dashboard)/self-evolution/page.tsx:1`） | `@multica/core` |
| Mobile | Expo SDK 55 + React Native 0.83.6（新架构、edge-to-edge）+ expo-router + NativeWind + FlashList | **自建 RN 原生屏**（`apps/mobile/app/**`，expo-router 文件路由） | `@multica/core`（类型/纯函数/queries） |

技术形态判定证据（实测·静态）：

- `apps/mobile/package.json:82` `"react-native": "0.83.6"`、`:58` `"expo": "~55.0.23"`；依赖含 `@react-navigation/native`、`@rn-primitives/*`、`@shopify/flash-list`、`nativewind-env.d.ts` 存在于仓——原生 RN 渲染栈。
- `apps/mobile/package.json` **无 `react-native-webview` 依赖**：主 UI 非 WebView/混合壳（判定标准：若为 WebView 形态，包体会出现 webview 运行时依赖或 `loadURL` 远端页面逻辑；实测两者皆无。grep `webview` 零命中）。
- `apps/mobile/app.config.ts:77` 插件 `expo-router`、`:137` `./plugins/with-android-share-intent`（Android 分享入口为原生 intent plugin，非 web 拦截）。

**对补齐路径的决定性约束（实测·仓库规范）**：`apps/mobile/CLAUDE.md`「What mobile may import from packages/」明文锁定 import 白名单——mobile 仅可 import `@multica/core` 类型与纯函数、`@multica/views/locales`（纯文案数据）。实测全仓引用吻合：mobile 对 `@multica/views` 的引用 18 处全部是 `@multica/views/locales`（i18n），对 `@multica/core` 引用 types×217、issues×38、agents×26、runtimes×13 等。

推论：**「复用 web views 组件」这一档在 mobile 上不存在**（被仓库规范禁止，且 views 为 DOM 实现）；mobile 唯一复用路径是 `@multica/core` 逻辑层 + 按 CLAUDE.md「Behavioral parity」四项红线（counts / permissions / state enums / data identity）做语义对齐的原生屏新建。下文所有「补齐路径」均以此为前提。Desktop 侧因直接复用 views，「复用 web」对 desktop 成立。

## 2. Owner 点名五域逐域盘点

每域按 Desktop 锚点 → 服务端锚点 → Mobile 现状 → 结论 → 补齐路径初判 组织。人日档位：S <5、M 5–15、L 15–30、XL >30。

### 域 1：Runtime 节点管理

- **Desktop 锚点**（实测）：
  - 路由：`apps/desktop/src/renderer/src/routes.tsx` — `runtimes`（DesktopRuntimesPage）、`runtimes/:id`（RuntimeDetailPage）、`runtimes/:id/runtime/:runtimeId`（RuntimeSettingsPage）；`apps/desktop/src/renderer/src/pages/runtime-detail-page.tsx:8` 直接复用 `@multica/views/runtimes` 的共享实现。
  - 能力面：`packages/views/runtimes/components/` — 全量 runtime 列表（`runtimes-page.tsx`）、机器 CLI 注册（`machine-cli-section.tsx`）、云 runtime 舰队（`cloud-runtime-dialog.tsx`）、runtime profiles 管理（`runtime-profiles-dialog.tsx`，管理员）、更新管理（`update-section.tsx`）、usage 图表（`usage-section.tsx`）、删除/解绑（`delete-runtime-dialog.tsx`）、重命名、语音实例创建/设置、自定义定价。
- **服务端锚点**（实测）：`server/cmd/server/router.go:2742` `/api/runtimes`（列表/手动注册/Patch/usage 三视角 2749–2751/更新触发/模型列表/本地 skills/凭据 PUT+DELETE/删除/解绑并删 2773 附近）；云舰队 `router.go:2785` 附近 `/api/cloud-runtime`（nodes CRUD）；daemon 任务协议 `router.go:1653–1685`。
- **Mobile 现状**（实测）：
  - `apps/mobile/app/(app)/[workspace]/more/runtimes.tsx:1–14` 文件头注释明示：列表**仅语音协议实例**（`isVoiceProtocolRuntime` 过滤），「CLI/daemon instances keep their existing surfaces」。
  - `more/runtimes/[id].tsx`（569 行）语音实例设置：改名/模型/高级参数/凭据 key 存清/删除；`more/runtimes/new.tsx`（371 行）语音实例注册（桌面 `voice-instance-create-dialog.tsx` 的 RN 镜像）。
- **结论：部分具备**。语音实例子集已具备（与桌面 `capabilities.realtime_voice` 对齐）；CLI/daemon 机器列表、CLI 注册引导、云舰队、runtime profiles、更新管理、模型列表、本地 skills、usage 图表、解绑删除流——mobile 缺失（对照锚点：上述 views/runtimes 组件与 `/api/runtimes` 各端点）。
- **补齐路径初判**：归类=移动端新增（服务端接口缺口：无）。`packages/core/runtimes/`（queries/mutations/access/profiles/models/local-skills/backpressure）已被 mobile 复用 13 处，逻辑层零缺口。人日档位 **M–L**。依赖：①RN 图表方案选型（usage/健康图）；②信息架构决策——扩展现有语音屏为全量 runtime 管理，还是新建全量页+语音页并存。

### 域 2：统计能力

- **Desktop 锚点**（实测）：路由 `usage` → `apps/desktop/src/renderer/src/routes.tsx`（`element: <DashboardPage />`，import 自 `@multica/views/dashboard`）；实现在 `packages/views/dashboard/components/dashboard-page.tsx`（usage 趋势卡、agent 排行 leaderboard、errors tab、过滤器）。
- **服务端锚点**（实测）：`server/cmd/server/router.go:2732` `/api/dashboard`：`usage/daily`、`usage/by-agent`、`agent-runtime`、`runtime/daily`、`failures/daily`、`failures/by-agent`；域内明细另有 runtime usage（2749–2751）、issue usage（2385）、skill usage（2660）、autopilot 配额（2509）。
- **Mobile 现状**（实测）：grep `usage|dashboard|analytics` 于 `apps/mobile/app`、`apps/mobile/data/queries` 零命中——无任何统计面。
- **结论：缺失**（对照前提：Desktop `usage` 路由 + `/api/dashboard` 六端点如上）。
- **补齐路径初判**：归类=移动端新增（服务端接口缺口：无）。人日档位 **M**。依赖：RN 图表库选型（`react-native-svg` 系）；手机端信息密度裁剪决策（建议趋势+排行+失败三项先行）。

### 域 3：空间设置/管理

- **Desktop/Web 锚点**（实测）：路由 `settings` → `packages/views/settings/components/settings-page.tsx:37–59`，tab 全集：profile（账户）、chat、issue、tokens（API 令牌）、authorizations（OAuth 授权）、workspace（空间改名/删除，`workspace-tab.tsx`+`delete-workspace-dialog.tsx`）、members（成员管理+邀请，`members-tab.tsx`）、repositories、integrations（GitHub/Slack/Telegram/DingTalk/Lark/Wecom/Composio/VCS）、labs、notifications、labels、issue-statuses、properties、quick-actions、keyboard-shortcuts、marketplace、billing；Desktop 另注入 daemon/updates 专属 tab（`routes.tsx` DesktopSettingsRoute）。
- **服务端锚点**（实测）：`router.go:1889–1890` `PUT/PATCH /api/workspaces/{id}`（UpdateWorkspace，owner/admin 闸门 1888）；成员 `1830` `GET /members`、`1891` `POST /members`（CreateInvitation）；statuses/labels/integrations 各自路由组同文件。
- **Mobile 现状**（实测）：`more/settings.tsx`（账户行、主题三选、工作区切换、应用更新检查、退出登录）+ 两个子页 `settings/profile.tsx`（257 行，改名+头像）、`settings/notifications.tsx`（204 行，分组通知开关）。无 workspace 改名/删除、无成员管理/邀请、无 labels/statuses/integrations/tokens/authorizations/billing/marketplace。grep `invit|workspace/members` 于 mobile app 屏层无管理面命中。
- **结论：部分具备**——个人设置面已具备；空间管理面缺失（对照锚点：settings-page.tsx 的 workspace/members/labels/issue-statuses/integrations/tabs 与对应 `/api/workspaces/{id}` 端点）。
- **补齐路径初判**：归类=移动端新增（服务端接口缺口：无）。分两档：空间管理核心（workspace 改名/删除 + members 列表/邀请/角色/移除 + labels + issue-statuses）档位 **M–L**；integrations 全家桶/billing/marketplace 建议产品决策判「保持 web-only」或极简只读（依赖：Owner/Leader 决策，见分期建议阶段 0）。

### 域 4：平台管理（超级管理员）

- **Desktop 锚点**（实测）：路由 `admin` → `packages/views/admin/admin-area.tsx`；子页 `admin-users-page.tsx`（用户列表/禁用/超管授予/impersonate）、`admin-workspaces-page.tsx`、`admin-oauth-clients-page.tsx`、`admin-mcp-server-page.tsx`、`impersonation-banner.tsx`。
- **服务端锚点**（实测）：`router.go:1793` `/api/admin`（`RequireHumanActor` + `RequireSuperAdmin`：users 列表/禁用/超管/impersonate、workspaces 列表/加成员、OAuth clients CRUD+rotate、grants 撤销、MCP status；写操作全部落 `admin_audit_log`）。
- **Mobile 现状**（实测）：无任何 admin 路由/屏。
- **结论：缺失**（对照前提：Desktop `admin` 路由 + `/api/admin` 全组）。
- **补齐路径初判**：归类=移动端新增（服务端接口缺口：无；`RequireHumanActor` 不排斥 mobile 会话）。人日档位 **M**。决策点：impersonation/超管授予等高危写操作是否开放到手机端（建议移动端一期只读+低危写，高危留桌面端；依赖：Owner/Leader 产品与安全决策）。

### 域 5：自进化能力体系

- **Desktop 锚点**（实测）：路由 `self-evolution` → `packages/views/self-evolution/components/self-evolution-page.tsx`，8 个 tab：overview、proposal（prompt 立法提案流）、quality（prompt 质量）、quiz、knowledge（知识库目录）、retrospective（每日复盘）、skill、versions；配套 `proposal-diff-view.tsx`、`proposal-preview-dialog.tsx`、`quality-compare-dialog.tsx` 等。
- **服务端锚点**（实测）：`router.go:2314` `/api/self-evolution`（overview 聚合）；`2251` `/api/prompt-legislation/proposals`（member 提案/提交/预打样 + owner 审批/驳回/恢复/enact）；`2276` `/api/retrospective`（读 member、config/run 写 owner）；`2290` `/api/knowledge`（读 member、登记/扫描/adopt owner）；prompt 目标态/恢复 `2245` 附近。
- **Mobile 现状**（实测）：无任何 self-evolution 路由/屏。
- **结论：缺失**（对照前提：Desktop `self-evolution` 路由 + 上述四组 API）。
- **补齐路径初判**：归类=移动端新增（服务端接口缺口：无）。全量档位 **L**；一期（overview 只读 + proposal owner 审批 + retrospective 查看）档位 **M**——owner 在移动端审批 prompt 提案是典型高频闭环场景，建议作为一期核心。依赖：proposal diff 在 RN 的渲染方案（现 `proposal-diff-view` 为 DOM 实现，需 RN 重写或降级为文本 diff）。

## 3. 点名之外的缺失面（mobile 缺、Desktop 已有，逐条附对照锚点）

| # | 缺失域 | Desktop/Web 对照锚点 | 服务端锚点 | 补齐档位初判 |
| --- | --- | --- | --- | --- |
| 1 | Skills 库管理（列表/详情/版本/usage/恢复） | `routes.tsx` `skills`+`skills/:id`；`packages/views/skills/` | `router.go:2650` 区段 `/api/skills`（usage 2660、版本 restore 2663） | M；mobile 仅有 agent 详情内单 agent skills 子页（`more/agents/[id]/skills.tsx`） |
| 2 | Autopilots（列表/详情） | `routes.tsx` `autopilots`+`autopilots/:id`；`packages/views/autopilots/` | `router.go:2505` `/api/autopilots`（quota 2509） | M |
| 3 | 成员详情/管理 | `routes.tsx` `members/:id`；settings `members-tab.tsx` | `router.go:1830/1891` | 归域 3 |
| 4 | API tokens / OAuth authorizations | settings `tokens-tab.tsx`、`authorizations-tab.tsx` | `/api/workspaces/{id}` 路由组 | S–M |
| 5 | Marketplace（安装/发布） | settings `marketplace-tab.tsx` + `packages/views/market/`、`packages/views/plugins/` | marketplace 相关 handler | 产品决策（可 web-only） |
| 6 | Billing/席位购买 | settings `billing-tab.tsx`、web `billing/return` 路由 | entitlement/seat 模块 | 建议保持 web-only（支付场景） |
| 7 | Integrations 全家桶 | settings 各集成 tab | 各集成路由组 | 归域 3，产品决策 |

Mobile 已具备、无需补齐的面（三端口径对照用）：chat、inbox、issues（详情/评论/picker/runs）、tasks 全空间任务 tab（RUYI-344）、决策中心 tab（RUYI-494/530）、projects（列表/详情/picker）、agents（列表/新建/详情+access/composio/custom-args/edit-profile/env/integrations/mcp/runtime-config/skills/webhooks 十个子页）、squads（列表/详情）、my-issues、pins、语音 runtime 实例管理、多服务器选择（`servers/`、`server-settings/`）、share-target（Android 分享接入）。

## 4. 差距矩阵总表（三端口径一致）

| 域 | Desktop/Web | Mobile | 服务端 API | 结论 |
| --- | --- | --- | --- | --- |
| 1 Runtime 节点管理 | 具备 | 部分（仅语音实例） | 具备 | **部分具备** |
| 2 统计能力 | 具备 | 缺失 | 具备 | **缺失** |
| 3 空间设置/管理 | 具备 | 部分（仅个人设置） | 具备 | **部分具备** |
| 4 平台管理（超管） | 具备 | 缺失 | 具备 | **缺失** |
| 5 自进化能力体系 | 具备 | 缺失 | 具备 | **缺失** |
| Skills / Autopilots / Tokens / Marketplace / Billing / Integrations | 具备 | 缺失（除 Integrations 无） | 具备 | **缺失**（点名外单列） |

核心事实：**五域服务端 API 全部已备，mobile 缺口全部是原生 UI 层**；不存在「服务端接口缺口」项。「复用 web」档仅对 desktop 成立；mobile 统一走「core 复用 + 原生屏新增」。

## 5. 分阶段实施建议

阶段 0（决策卡，不写码）：需 Leader/Owner 裁决——①integrations/billing/marketplace 是否移动端化或保持 web-only；②admin 高危写操作（impersonate/超管授予）是否开放移动端；③RN 图表库选型；④runtime 全量管理的信息架构（扩展语音屏 vs 新建全量页）。

| 阶段 | 目标 | 先后依据 | 验收要点 |
| --- | --- | --- | --- |
| 1 | 统计能力 usage 屏 | 纯只读新增、无权限复杂度、Owner 点名、用户价值直接 | 趋势/排行/失败三视图数值与 web 同 API 同窗口一致（behavioral parity：counts）；空态/无权限态 |
| 2 | Runtime 节点管理补全 | 已有语音实例底座 + `core/runtimes` 全备，增量最小 | 列表覆盖 CLI/daemon/语音三类（对照 `runtimes-page.tsx`）；凭据 tri-state、解绑删除确认流与桌面语义对齐（对照 `delete-runtime-dialog.tsx` 契约） |
| 3 | 空间管理核心（workspace/members/labels/statuses） | Owner 点名；管理动作是移动闭环刚需 | 邀请/角色/移除走与 web 相同 mutations（core 复用）；workspace 删除需二次确认+权限闸门一致 |
| 4 | 自进化一期（overview+proposal 审批+retrospective 查看） | Owner 点名；owner 移动审批是高频决策场景 | 审批动作命中 `POST /{id}/approve` 同一权限面（owner-only）；只读 tab 不泄漏 owner 闸门数据 |
| 5 | Skills/Autopilots 移动端 | 点名外补齐，扩大闭环 | 列表/详情/基础操作 parity；usage 口径与 web 一致 |
| 6 | Admin（只读先行） | 依赖阶段 0 决策 ② | users/workspaces 只读列表+审计可见；高危写按决策结论处置 |

每阶段通用验收（源自 `apps/mobile/CLAUDE.md` 既有红线）：counts/permissions/state enums/data identity 四项 parity + WS 事件覆盖与 web 一致；涉 owner/admin 权限的屏必须验证权限闸门在 UI 层不可达（非仅隐藏）。

## 6. 交付信息

- 载体：平台分配 worktree，分支 `agent/agent-f70e39b85abd/ruyi-638`；本报告为唯一新增产物（文档），无源码 delta。
- 主检出 `/home/guxy/Codes/official/multica` 全程只读，未改动。
- 未实测项声明：所有结论为静态代码锚点实测；真机渲染、接口实调、图表行为未实测（本阶段禁运行）。
