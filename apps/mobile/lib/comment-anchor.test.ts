// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TimelineEntry } from "@multica/core/types";
import { buildTimelineRows } from "./timeline-thread";
import { resolveCommentAnchor } from "./comment-anchor";

/**
 * 评论锚点决策（RUYI-108）。
 *
 * 两条硬约束在这里锁死：
 *   1. 回复也能被引用——引用一条回复必须解析出它所属的 root（要展开的是
 *      root，要闪的是回复本身），否则折叠线程里的回复永远定位不到。
 *   2. 查不到就是查不到——不区分「已删除 / 无权限 / 跨任务单 / 未加载」，
 *      任何区分都会把一条不可见评论的存在性透给点击者。
 */
const comment = (id: string, parentId: string | null = null): TimelineEntry =>
  ({
    type: "comment",
    id,
    actor_type: "member",
    actor_id: `u-${id}`,
    content: `c-${id}`,
    parent_id: parentId,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    comment_type: "comment",
    reactions: [],
    attachments: [],
  }) as TimelineEntry;

describe("resolveCommentAnchor", () => {
  it("引用根评论：展开自身并高亮自身", () => {
    const rows = buildTimelineRows([comment("a"), comment("b")]);
    expect(resolveCommentAnchor(rows, "a")).toEqual({
      kind: "focus",
      rootId: "a",
      commentId: "a",
    });
  });

  it("引用回复：展开所属 root，高亮的仍是被引用的那条回复", () => {
    const rows = buildTimelineRows([
      comment("a"),
      comment("r1", "a"),
      comment("r2", "r1"),
    ]);
    expect(resolveCommentAnchor(rows, "r2")).toEqual({
      kind: "focus",
      rootId: "a",
      commentId: "r2",
    });
  });

  it("目标不在已加载数据里 → 降级，且不区分原因", () => {
    const rows = buildTimelineRows([comment("a")]);
    // 已删除 / 无权限 / 跨任务单 / 尚未加载，对外是同一个结果。
    for (const id of ["deleted-1", "other-issue-comment", "unknown"]) {
      expect(resolveCommentAnchor(rows, id)).toEqual({ kind: "unavailable" });
    }
  });

  it("空 id 与空时间线都按降级处理，不抛错", () => {
    expect(resolveCommentAnchor([], "a")).toEqual({ kind: "unavailable" });
    expect(resolveCommentAnchor(buildTimelineRows([comment("a")]), "")).toEqual(
      { kind: "unavailable" },
    );
  });
});
