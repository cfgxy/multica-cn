/**
 * 评论锚点点击的决策（纯函数，RUYI-108）。
 *
 * `mention://comment/<id>` 的点击只有两种结局：目标在当前时间线里 → 展开其
 * 所属 root 并定位高亮；查不到 → 降级反馈。这里只做判定，副作用（展开、
 * 滚动、触感）留在组件层。
 *
 * 「查不到」刻意不区分原因——已删除、无权限、跨任务单、数据尚未加载，对外
 * 都是同一个 `unavailable`。区分即泄露：告诉点击者「这条存在但你看不到」
 * 已经暴露了一条不可见评论的存在性。同理，这里不发起任何请求：
 * 只查调用方交进来的、当前已加载的行。
 */
import { resolveExpandRootId } from "./comment-locate";
import type { TimelineRow } from "./timeline-thread";

export type CommentAnchorOutcome =
  /** 目标可见：`rootId` 需要展开并滚动到位，`commentId` 是要闪高亮的那条
   *  （可能是 root 自身，也可能是它下面的某条回复）。 */
  | { kind: "focus"; rootId: string; commentId: string }
  /** 目标不可用：降级反馈，不显示作者与摘要，不发请求。 */
  | { kind: "unavailable" };

export function resolveCommentAnchor(
  rows: readonly TimelineRow[],
  commentId: string,
): CommentAnchorOutcome {
  if (!commentId) return { kind: "unavailable" };
  const rootId = resolveExpandRootId(rows as TimelineRow[], commentId);
  if (!rootId) return { kind: "unavailable" };
  return { kind: "focus", rootId, commentId };
}
