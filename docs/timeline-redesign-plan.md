# Timeline 重设计计划

> **状态(2026-05-07)**:Phase 2 / 3 / 4 已实现并提交。Phase 5(filter)按需延后。
> 看 `git log feat/timeline-comment-anchored-pagination` 获取每阶段的具体提交。


## 问题

当前 issue timeline 用「每页 50 条 entry(comment + activity 共享配额)」的 cursor 分页(#1968 / #2128)。这套设计:

- 解决了「长 issue 一次性返回上千条直接卡浏览器」的原始问题
- 但因为 Multica 是 agent-heavy 产品,activity 行数远多于 comment,导致**comment 经常被 activity 挤出第一页**
- #2192 把「评论永久不可达」修成「评论可达但要点很多次」
- #2204 加 `×N` 角标,只是症状治理

根因:**分页配额按 DB 行算,而不是按「信息单元」算。** comment 是稀缺信号、activity 是噪声,不该平权。

## 终态

- 后端按 **comment 数**分页(默认 20/页)
- activity 跟随 comment 时间窗口返回,**不占独立配额**
- 单次响应 hard cap = 500 activity,超过部分通过单独 endpoint 懒加载
- 前端把任意 ≥3 条连续 activity 折叠为一行(带摘要),点击就地展开
- around 模式(inbox 跳转)支持 anchor 在折叠群里的 activity → 自动展开 + scroll

任何规模的 issue 首屏都能看到完整对话流,长 issue 不卡浏览器(#2128 成果保留)。

## 不变量

1. 首屏必有 comment(只要 issue 有 comment)
2. 任意规模 issue 不卡浏览器
3. 单次 timeline 响应 < 200KB
4. 折叠群可还原(无损,展开看到逐条原始 activity)
5. 老桌面端走 `listTimelineLegacy` 不受影响

## 分阶段交付

### Phase 2:前端折叠放宽(短期止血,1 天)

**改动**:`packages/views/issues/components/issue-detail.tsx:303-322`

- 折叠条件从「同 actor + 同 action + 2min」放宽为「任意 ≥3 条连续 activity 折叠为一群」
- 折叠群可点击展开,展开状态保留(useState)

**收益**:屏幕信息密度立刻改善,2000 条 activity 视觉上变成 ~30 个折叠群。
**仍不解决**:分页配额仍按 DB 行算,200 条连续 activity 仍占 4 页。

**测试**:
- `packages/views/issues/components/issue-detail.test.tsx` 加用例:50 条异构 activity 折叠为 1 群
- 折叠群展开/收起的交互测试

### Phase 3:后端按 comment 分页(根治,2-3 天)

**API 契约**:

```
GET /issues/:id/timeline?comment_limit=20

→ {
    comments:    [...]                    // 最新 N 条 comment(DESC)
    activities:  [...]                    // 时间窗口内所有 activity(DESC)
    activity_truncated_count?: number     // 仅当超过 hard cap=500
    has_more_before: bool                 // 还有更老的 comment
    has_more_after:  bool
    next_cursor?: string                  // comment 游标
    prev_cursor?: string
  }

GET /issues/:id/timeline?around=<id>&comment_limit=20

→ {
    comments:    [...]                    // ~10 条 older + ~10 条 newer + (anchor 是 comment 时)anchor 自己
    activities:  [...]
    target: {
      id: "<entry_id>",
      type: "comment" | "activity",
      activity_group_index?: number       // anchor 是 activity 时,前端展开第几个折叠群
    },
    ...
  }
```

**后端实现**(`server/internal/handler/activity.go`):

1. 新增 `comment_limit` query param,与 `limit` 互斥
2. 新增 `listTimelineLatestV2` / `listTimelineBeforeV2` / `listTimelineAfterV2` / `listTimelineAroundV2`,旧函数保留作为 fallback
3. 每个 V2 函数:
   - 先按 comment 数取 N 条 comment
   - 取 `oldest_comment.created_at` 作为时间窗下界(latest 模式;before 模式还要加 cursor 上界)
   - 拉时间窗内的 activity,limit=500(hard cap)
   - 计算 `activity_truncated_count`(查 `COUNT(*) WHERE in_window` 减去返回数,或者多查 1 行确认)
4. `around` 模式
   - anchor 是 comment:正常居中
   - anchor 是 activity:找最近的 comment,以它为锚点居中
   - 极端 case(0 comment):返回 `comments=[]`,`activities=` 围绕 anchor 50 条
5. 新增 SQL: `ListActivitiesInWindow`(`WHERE issue_id=? AND created_at >= ? AND created_at < ? ORDER BY ... LIMIT 500`)
6. 新增 SQL: `ListCommentsLatestN` / `ListCommentsBeforeN`(已有,改名/重用)

**前端实现**:

- `packages/core/api/schemas.ts`:`TimelineResponseSchema` 新增 `comments` / `activities` / `activity_truncated_count` / `target`
- `packages/core/issues/queries.ts`:`issueTimelineInfiniteOptions` 改成发新接口,适配 cursor 字段
- `packages/views/issues/hooks/use-issue-timeline.ts`:flatten 逻辑改成「按 comment 时间穿插 activity」
- `packages/views/issues/components/issue-detail.tsx`:折叠群组件接受 `forceExpanded` prop,处理 around 模式
- 新增懒加载 endpoint:`GET /issues/:id/activities?before=...&limit=200`,前端折叠群尾部加「加载剩余 N 条」按钮

**API 版本管理**:

- 新接口走 query param `?comment_limit=...`,不存在时退回旧的 `?limit=...` 行为(保留 v0.2.26+ 老客户端兼容)
- 桌面 v0.2.26+ 直接发 `comment_limit=20`
- web app 直接发新参数(随服务端部署)
- pre-#2128 客户端继续走 `listTimelineLegacy`(由 query 字符串完全为空触发)

**测试**:
- `server/internal/handler/activity_test.go` 加 V2 用例覆盖三种 anchor 类型 + 极端规模
- `packages/core/issues/queries.test.ts` 验证 cursor 推进
- `packages/views/issues/hooks/use-issue-timeline.test.tsx` 验证 around 模式展开折叠群

### Phase 4:activity 硬 cap + 懒加载(防御,0.5 天)

**前提**:Phase 3 已上线,`activity_truncated_count` 字段已可用。

**改动**:
- 服务端 `GET /issues/:id/activities?before=<cursor>&limit=200` 端点(纯 activity cursor 分页,不含 comment)
- 前端折叠群组件接受 `truncated_count` prop,渲染「加载剩余 N 条」按钮
- 点击 → fetch 单独 endpoint → setState 把新 activity merge 进折叠群

**测试**:
- 模拟 1500 条 activity 的 issue,验证首屏 <200KB,展开折叠群后底部出现按钮
- 点击按钮验证懒加载 200 条

### Phase 5(可选,延后):filter

仅当真有用户反馈再做。Comments / Activity / All 三档切换,作为「超大规模 issue 的逃生口」。

## 关键文件 / 位置参考

| 改动点 | 文件 | 当前行号 |
|---|---|---|
| 后端 timeline handler | `server/internal/handler/activity.go` | 全文件 |
| `hasMoreBeyond` 公式 | 同上 | 462 |
| 后端 SQL: comments | `server/pkg/db/queries/comment.sql` | 26+ |
| 后端 SQL: activities | `server/pkg/db/queries/activity.sql` | 9-21 |
| 前端 infinite query | `packages/core/issues/queries.ts` | 149 |
| 前端 timeline hook | `packages/views/issues/hooks/use-issue-timeline.ts` | 67+ |
| 前端折叠 + 渲染 | `packages/views/issues/components/issue-detail.tsx` | 303-322, 985+ |
| API schema | `packages/core/api/schemas.ts` | (Timeline 部分) |

## 风险 + 待定

1. **Inbox 跳转回归**:Phase 3 必须**同步**实现 around 模式的折叠群展开,否则 v0.2.27+ 用户点 inbox activity 通知会找不到目标。**作为 Phase 3 子任务,不可延后。**
2. **Activity 摘要句**:折叠群展示「47 次状态变化 by @claude-code」需要前端聚合逻辑。如果同群内 actor 或 action 异构,摘要规则待定:
   - 选项 A:列前 2 个 actor + ` 等 N 人`,列前 2 种 action + ` 等 M 类`
   - 选项 B:`47 个系统事件`,无聚合
   - 推荐 A,实现简单且信息量更大
3. **WS 实时更新与折叠群**:新 activity 通过 WS 到达时,query invalidate 重拉整页。如果用户当前正展开某折叠群,展开状态会丢失。需要:
   - 折叠群展开状态 keyed by 群「锚点 comment id」(而非 index),重拉后能恢复
4. **`activity_truncated_count` 性能**:精确计数需要额外 `COUNT(*)` query。可以用「多查 1 行」近似,显示「500+ 系统事件」。推荐近似法。
5. **API 版本切换**:新老客户端共存期间,服务端要同时维护新旧 handler。旧 handler 在所有 v0.2.26 客户端淘汰后(预计 1-2 个月)删除。

## 推荐执行顺序

1. **本 PR**:Phase 2(前端折叠放宽)—— 最小改动,立刻改善体验
2. **下个 PR**:Phase 3 后端 + 前端 + Inbox around 模式(一起,不能拆)
3. **Phase 3 上线后**:Phase 4 懒加载
4. **Phase 5**:延后,看用户反馈

## 不做的事

- ❌ 分两栏(左 comment 右 activity)
- ❌ 默认隐藏 activity
- ❌ WS 实时聚合(activity 一来就更新折叠群计数)—— 接受 invalidate 重拉
- ❌ 把 `(comment_id, activity_id)` 双游标暴露给客户端(过度复杂,单 comment 游标够用)
