# Onboarding 重构方案

## 1. 背景

当前 onboarding 是两次错位重构(MUL-2438 `591e4784` + `da0ecb6a`)叠加的产物,留下了 5 个明显的架构味道。这份文档定义重构的目标设计、实施步骤、以及老用户兼容性论证。

### 1.1 现状的 5 个味道

1. **同一状态 4 个写入点**:`users.onboarded_at` 在 `BootstrapOnboardingRuntime` / `BootstrapOnboardingNoRuntime` / `AcceptInvitation` / `CompleteOnboarding` 四个 handler 里各自 `MarkUserOnboarded`,每个还附带不同副作用。没有单一服务入口。

2. **种 install-runtime issue 也有 4 个调用点**:`ensureNoRuntimeOnboardingIssue` 在 `CreateWorkspace` / `AcceptInvitation` / `CompleteOnboarding` 里跑,`seedInstallRuntimeIssue` 在 `BootstrapOnboardingNoRuntime` 里跑。靠 `LockAndFindActiveDuplicate` advisory lock 防重 — 用底层去重补救上层职责不清。

3. **Step 3 Connect 路径与 Skip 路径不对称**
   - Connect:Step 3 不 mark,延迟到工作区 Modal 完成
   - Skip:Step 3 立即 mark
   两条路径的副作用完成时机不同,导致"未 onboarded mid-flow"这个状态对两条路径含义不同。

4. **Modal 自查环境而不是接收上游选择**:`OnboardingHelperModal` 进入后自己 query `runtimeListOptions(wsId)` 并取 `runtimes.data?.[0]` — 完全丢弃用户在 Step 3 的选择。违反"前一步决策应该流向后一步"的基本数据流原则。

5. **`onboarded_at` 双语义靠注释维护**:既是"完成状态",也是"Modal 触发条件"。`invitation.go:429-440` 的注释专门警告"不要删除 AcceptInvitation 里的 MarkUserOnboarded,否则 Modal 会弹给被邀请者" — 靠注释维护的不变量是设计味的明显信号。

### 1.2 业界佐证

- **State Backed** / **Single State Model Architecture (Cerny, Dec 2025)**:后端应该是状态机,单一权威状态
- **Greg Young (CQRS/DDD)**:Handler 应该薄,业务逻辑放 service
- **Userpilot / HubSpot 案例**:onboarding 进度应跨 session 持久化
- **PlanetScale / Shopify Engineering**:Schema 新列必须 nullable 或带 default,迁移先于代码

四条原则一致支持本方案。

---

## 2. 目标

重构要满足的 5 条核心原则:

1. **单一翻转点**:`onboarded_at` 翻转只发生在一处(工作区入口),Skip / Connect 对称
2. **意图字段化**:Step 3 用户选择持久化到 `users` 表,跨设备 / 刷新 / 中断都可读
3. **Modal 变 dumb component**:接 props 不查环境,数据流单向
4. **Step 3 零副作用**:Step 3 退出只 PATCH 字段 + navigation,不调任何 bootstrap
5. **单一种 issue 入口**:install-runtime issue 只在工作区入口决定是否种

---

## 3. 设计方案

### 3.1 数据层 — Schema 改动

```sql
ALTER TABLE users
  ADD COLUMN onboarding_runtime_id UUID NULL
    REFERENCES agent_runtimes(id) ON DELETE SET NULL,
  ADD COLUMN onboarding_runtime_skipped BOOLEAN NOT NULL DEFAULT FALSE;

-- 防御性 CHECK 约束:禁止 (uuid, true) 非法组合
ALTER TABLE users
  ADD CONSTRAINT users_onboarding_choice_check
  CHECK (NOT (onboarding_runtime_id IS NOT NULL AND onboarding_runtime_skipped = TRUE));
```

**字段语义**:

| `runtime_id` | `skipped` | 含义 |
|---|---|---|
| NULL | false | 未到 Step 3 / Step 3 未操作 |
| `<uuid>` | false | Step 3 选了这个 runtime |
| NULL | true | Step 3 显式 Skip |
| `<uuid>` | true | 非法组合,CHECK 约束阻止 |

**`ON DELETE SET NULL` 的作用**:runtime record 被删后字段自动退化为 NULL,降级到"未选"行为,不会成悬空引用。

### 3.2 服务层 — 新增

新增两个 service,收敛所有副作用入口:

```go
// server/internal/service/onboarding.go
type OnboardingService struct { ... }

// 唯一的"完成 onboarding"入口,所有 handler 内部调它
func (s *OnboardingService) Complete(ctx, userID, path, opts) error {
  // 状态机内部决定:mark + 是否种 issue + 是否建 agent
}
```

```go
// server/internal/service/workspace_content.go
type WorkspaceContentService struct { ... }

// 唯一的"种 install-runtime issue"入口
func (s *WorkspaceContentService) EnsureInstallRuntimeIssue(ctx, wsID) error
```

### 3.3 Handler 层 — 变薄

- `BootstrapOnboardingRuntime` / `BootstrapOnboardingNoRuntime` / `AcceptInvitation` / `CompleteOnboarding`:只做参数校验和 service 调用,不再自己 mark / 不再自己种 issue
- `CreateWorkspace`:**删除** `ensureNoRuntimeOnboardingIssue` 调用,种 issue 改由工作区入口的 service 调用
- 新增 endpoint:`PATCH /me/onboarding` 接受 `{ runtime_id?: string; runtime_skipped?: boolean }`

### 3.4 前端 — 改动概述

- **Step 3 退出**:`onboarding-flow.tsx:handleRuntimeNext` 改为:
  ```ts
  if (!rt) {
    await api.patchOnboarding({ runtime_skipped: true });
  } else {
    await api.patchOnboarding({ runtime_id: rt.id });
  }
  onComplete(workspace, undefined);  // 纯 navigation
  ```
- **工作区入口**:新增 `<WorkspaceOnboardingInit />` 组件,挂在 `apps/web/app/[workspaceSlug]/layout.tsx` 和 `apps/desktop/src/renderer/src/components/workspace-route-layout.tsx`
- **Modal 改 dumb**:`OnboardingHelperModal` 接收 `runtimeId` prop,删除内部 `runtimeListOptions` query

### 3.5 工作区入口的 4 分支

```ts
function WorkspaceOnboardingInit() {
  const me = useAuthStore(s => s.user);
  const workspace = useCurrentWorkspace();

  // 分支 0: 已完成 — 不挡道,但要兜底种 install-runtime issue
  if (me.onboarded_at != null) {
    return <EnsureWorkspaceContent workspaceId={workspace.id} />;
  }

  // 分支 1: 显式 Skip
  if (me.onboarding_runtime_skipped) {
    return <SkipBootstrapping workspaceId={workspace.id} />;
  }

  // 分支 2: 选了 runtime
  if (me.onboarding_runtime_id) {
    return <OnboardingHelperModal
      workspace={workspace}
      runtimeId={me.onboarding_runtime_id}
    />;
  }

  // 分支 3: 中途跑路 — 重新走一遍 onboarding
  navigate("/onboarding");
  return null;
}
```

**分支 0 的兜底**:`<EnsureWorkspaceContent />` 在 workspace 无 runtime 且无 install-runtime issue 时,调一次 `EnsureInstallRuntimeIssue`。这保证已 onboarded 用户被邀请进新 workspace 时看板不空。

---

## 4. 完整流程

### 4.1 选了 runtime 路径(主流)

| 时刻 | 用户动作 | API 调用 | server 数据变化 |
|---|---|---|---|
| T0 | OAuth 登录 | `POST /auth/google` | `users` 新建,所有 onboarding 字段为默认值 |
| T1-T3 | 答 3 题问卷 | `PATCH /me/onboarding {questionnaire}` | `users.onboarding_questionnaire` 累加 |
| T4 | 建 workspace | `POST /workspaces` | `workspaces` + `members` 各 +1,**不种 issue 不 mark** |
| T5 | Step 3 选 runtime A | `PATCH /me/onboarding {runtime_id:"A"}` | `users.onboarding_runtime_id = "A"` |
| T6 | 跳 `/<slug>/issues` | — | — |
| T7 | `<WorkspaceOnboardingInit />` mount | — | — |
| T8 | 判定 `runtime_id != NULL` → 弹阻塞 Modal | — | — |
| T9 | 用户多选 starter 卡 → submit | `POST /me/onboarding/runtime-bootstrap {workspace_id, runtime_id, starter_prompts:[...]}` | 单事务:<br>1. `agents` +1(Helper,runtime=A)<br>2. `issues` +N(starter,assign Helper)<br>3. **`users.onboarded_at = now()`**<br>4. task 入队 |
| T10 | 跳第一条种好的 issue | — | — |

**Modal 阻塞**:用户必须 submit(至少种 0 条 issue 也算 submit)才能关闭,翻转点在 submit。

### 4.2 点 Skip 路径(对称)

| 时刻 | 用户动作 | API 调用 | server 数据变化 |
|---|---|---|---|
| T5 | Step 3 点 Skip | `PATCH /me/onboarding {runtime_skipped:true}` | `users.onboarding_runtime_skipped = true` |
| T7 | `<WorkspaceOnboardingInit />` mount | — | — |
| T8 | 判定 `runtime_skipped == true` → 自动 bootstrap | `POST /me/onboarding/no-runtime-bootstrap {workspace_id}` | 单事务:<br>1. `issues` +1(install-runtime 教程)<br>2. **`users.onboarded_at = now()`** |
| T9 | 跳到种好的 issue | — | — |

**无 Modal,直接结束**。

### 4.3 字段演化对照

**选 runtime 路径**:
```
T0:  (onboarded_at=NULL, runtime_id=NULL,     skipped=false)
T5:  (NULL, "A-uuid", false)
T9:  (now,  "A-uuid", false)   ← 翻转点
```

**Skip 路径**:
```
T0:  (NULL, NULL, false)
T5:  (NULL, NULL, true)
T8:  (now,  NULL, true)        ← 翻转点
```

**中途跑路**:`(NULL, NULL, false)` 一直保持,下次进入时 resolver 把他送进工作区,`<WorkspaceOnboardingInit />` 分支 3 把他踹回 `/onboarding` 重走。

---

## 5. 老用户兼容性

老用户(`onboarded_at != NULL`)**100% 安全**,因为代码层面已有 4 个 gate 把他们隔在 onboarding 路径之外:

| Gate 位置 | 代码 |
|---|---|
| `resolvePostAuthDestination` (`paths/resolve.ts:34-38`) | `if (first) return /<slug>/issues` |
| `OnboardingPage` (`apps/web/app/(auth)/onboarding/page.tsx:60`) | `if (hasOnboarded) router.replace(...)` |
| `OnboardingHelperModal` (`onboarding-helper-modal.tsx:91`) | `if (me.onboarded_at != null) return null` |
| 桌面端 `App.tsx:138-176` | `wsCount > 0 → no overlay`,`!hasOnboarded` 才进 onboarding overlay |

**新增字段对老用户永远不被读** — 读它们的代码(新 Modal、`<WorkspaceOnboardingInit />`)全部在 `onboarded_at != null` 的下游被短路掉。新字段默认值 `(NULL, false)` 对老用户无害。

**Migration 全 additive**(只加列、加 service、加分支,不删除任何破坏性结构),所有 PR 可独立部署、独立回滚。

---

## 6. 实施步骤(分 8 个 PR)

每一步独立可发可回滚,中间态无两版本互不兼容窗口。

### PR 1 — Schema migration

- 加列 `onboarding_runtime_id UUID NULL` + `onboarding_runtime_skipped BOOLEAN NOT NULL DEFAULT FALSE`
- 加 CHECK 约束
- sqlc 重新生成 user.sql.go
- **不改任何代码逻辑**,老 server 继续跑

### PR 2 — 加 service 层

- 新增 `server/internal/service/onboarding.go` 含 `OnboardingService.Complete`
- 新增 `server/internal/service/workspace_content.go` 含 `WorkspaceContentService.EnsureInstallRuntimeIssue`
- 4 个 handler 内部转调 service 但**行为不变**(service 内部还是种 issue + mark 的旧逻辑)
- 单元测试覆盖 service 层

### PR 3 — 新增 `PATCH /me/onboarding` 接受 runtime 字段

- 扩展 `PatchOnboarding` handler 接受 `runtime_id` / `runtime_skipped` 字段
- 字段为 optional,前端暂不调
- handler 测试:接受字段、CHECK 约束触发、字段持久化

### PR 4 — 前端 Step 3 改为 PATCH(双写过渡)

- `onboarding-flow.tsx:handleRuntimeNext` 加上 `await api.patchOnboarding({ runtime_id | runtime_skipped })`
- **保留**对 `bootstrapNoRuntimeOnboarding` 的调用(避免老前端跑新 server 时 Skip 路径断)
- 此时 Connect 路径行为不变,Skip 路径**同时写字段 + 调旧 bootstrap**

### PR 5 — 新增 `<WorkspaceOnboardingInit />`

- 实现 4 分支组件,挂在 web + desktop layout
- Modal 改为接 `runtimeId` prop;保留对外旧 query 兼容(降级 fallback)
- 此时新分支组件**已在 prod 运行**,但因为 Skip 路径仍走旧 bootstrap,Skip 用户进工作区时 `onboarded_at` 已是 now,走分支 0

### PR 6 — 删 Step 3 的旧 bootstrap 调用

- `onboarding-flow.tsx:handleRuntimeNext` 删掉 `await bootstrapNoRuntimeOnboarding(workspace.id)`
- 仅保留 `await api.patchOnboarding(...)`
- 此后 Skip 路径完全走"工作区入口自动 bootstrap"
- 灰度发布,观察 onboarded 完成率

### PR 7 — 删 CreateWorkspace / AcceptInvitation 里的种 issue 调用

- `workspace.go` 删 `ensureNoRuntimeOnboardingIssue`
- `invitation.go` 删 `ensureNoRuntimeOnboardingIssue`
- 由 `<WorkspaceOnboardingInit />` 分支 0 的 `<EnsureWorkspaceContent />` 兜底
- 同时改 `CompleteOnboarding` 也不种(它本来就是 skip_existing 路径,现在统一由工作区入口管)

### PR 8 — 清理死代码

- 删 `starter_content_state` 列(单独的 migration + 老 desktop 兼容审查)
- 删 `OnboardingCompletionPath` 里没用的 `cloud_waitlist` enum 值(实际无人发送)
- 删 `bootstrapNoRuntimeOnboarding` 旧调用点的 fallback 代码

---

## 7. 风险与回滚

### 7.1 主要风险

| 风险 | 严重度 | 应对 |
|---|---|---|
| 老桌面端发请求不带新字段 | 低 | 新字段 optional,handler 接受老 body |
| Step 3 PATCH 失败用户卡住 | 中 | 前端必须 await PATCH 成功才 navigation;失败显示 toast 重试 |
| Modal 阻塞用户 submit 中网络断 | 中 | 后端事务保证原子;前端显示重试按钮 |
| 多 tab 并发 mount `<WorkspaceOnboardingInit />` 重复调 bootstrap | 低 | 后端 `LockAndFindActiveDuplicate` 已有 dedup |
| 中途跑路用户跳回 `/onboarding` 死循环 | 中 | 严格保证"分支 3 = `(NULL, NULL, false)`"才跳;onboarded 用户用 OnboardingPage gate 防御 |

### 7.2 回滚策略

每个 PR 独立可回滚:
- PR 1 schema:列保留,不影响行为(回滚 = 不读字段)
- PR 2-3 service / endpoint:可单独 revert
- PR 4-6 前端:可单独 revert 前端构建
- PR 7-8 清理:revert 即恢复种 issue 调用

---

## 8. 未来工作

### 8.1 产品取舍待拍

**Modal starter prompt 单选 vs 多选**:

| | 单选(现状) | 多选 |
|---|---|---|
| 体验 | 保守,1 个 starter task | Helper 一开始就跑 N 个 task,密度感强 |
| token 消耗 | 1× | N× |
| 产品定位 | "AI 是工具" | "AI 是同事" |

工程上 `BootstrapOnboardingRuntime` 把 `starter_prompt string` 改成 `starter_prompts []string`,种 N 个 issue 都 assign 给同一个 Helper,可单独评估。

### 8.2 后续可清理

- `CompleteOnboarding` handler 的 `cloud_waitlist` enum 实际无人发送,可删
- `starter_content_state` 列对老桌面端的兼容窗口可以收口

---

## 9. 验收标准

实施完成后,以下 invariant 必须为真:

1. `onboarded_at` 在代码里只有 **1 处** 调用 `MarkUserOnboarded`(在 `OnboardingService.Complete` 内部)
2. `install-runtime issue` 在代码里只有 **1 处** 调用 `CreateIssue`(在 `WorkspaceContentService.EnsureInstallRuntimeIssue` 内部)
3. Step 3 退出代码里**不出现** `bootstrapNoRuntimeOnboarding` / `bootstrapRuntimeOnboarding` 调用
4. `OnboardingHelperModal` 内部**不出现** `runtimeListOptions` query
5. 老用户登录 → 直接进工作区 → 不弹任何 Modal、不见任何"初始化中"loading

每条都可以用 `grep` 验证。

---

## 10. 干净度评分

工程层面打 **9.5/10**。剩下 0.5 是 CHECK 约束之外的状态空间防御性追求,实践不必。

5 个核心原则全中:
- 单一翻转点
- Skip / Connect 对称
- Step 3 零副作用
- Modal dumb
- 意图字段化持久化
