// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  COMMENT_HIGHLIGHT_FADE_MS,
  COMMENT_HIGHLIGHT_HOLD_MS,
  COMMENT_HIGHLIGHT_TOTAL_MS,
} from "./comment-highlight";

/**
 * 评论高亮时长（RUYI-108）。
 *
 * 这里锁的不是「某个数字等于某个数字」，而是三端唯一一件真正共享的东西——
 * 时间表本身。返工前 Web 是 2500ms 状态 + 700ms 过渡（可见 3.2s），Mobile 是
 * 700+1800+700（同样 3.2s），而它的上游闸门 5s 后才放开：三处各写各的，
 * 谁都不知道自己在参与同一个约定。
 */
describe("评论高亮时间表", () => {
  it("完整可见时长落在任务要求的 1–2 秒内", () => {
    // 上界是验收条款；下界防止「合规地缩到 200ms」——滚动动画还没停就闪完了，
    // 等于没有高亮。
    expect(COMMENT_HIGHLIGHT_TOTAL_MS).toBeLessThanOrEqual(2000);
    expect(COMMENT_HIGHLIGHT_TOTAL_MS).toBeGreaterThanOrEqual(1000);
  });

  it("总时长 = 保持 + 一次淡出，且淡入淡出装得进保持窗口", () => {
    expect(COMMENT_HIGHLIGHT_TOTAL_MS).toBe(
      COMMENT_HIGHLIGHT_HOLD_MS + COMMENT_HIGHLIGHT_FADE_MS,
    );
    // Mobile 的动画序列是「淡入 → 停 → 淡出」，中间那段停留由总时长减去两次
    // 淡出算出。淡出时间一旦超过总时长的一半，这个差值变负，Reanimated 的
    // withDelay 会立刻收到负延迟，高亮直接跳过停留阶段。
    expect(COMMENT_HIGHLIGHT_TOTAL_MS - 2 * COMMENT_HIGHLIGHT_FADE_MS)
      .toBeGreaterThan(0);
  });
});
