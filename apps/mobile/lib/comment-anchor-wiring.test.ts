// @vitest-environment node
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * 手机端评论锚点落地的接线（RUYI-108 第二轮返工）。
 *
 * 落地是否正确取决于三处接线，而三处都跑不进 node lane 的单测里：时间线组件
 * 依赖 FlashList 与 RN 原生模块，卡片依赖 Reanimated。判定逻辑本身已在
 * `comment-locate.test.ts` / `comment-target-visibility.test.ts` /
 * `comment-geometry.test.ts` 里按纯函数覆盖；这里只钉住「逻辑确实被接上」，
 * 这是本仓能观察到接线断裂的唯一层。
 *
 * 要钉住的三件事：
 *   1. 点击引用时把**目标评论 id**（可能是回复）交给 focus 意图，而不是只交
 *      root——只交 root，控制器就退回行级判定，长线程里目标留在屏外。
 *   2. 展开发生在启动定位之前——折叠的 root 里回复根本没渲染，量不到位置。
 *   3. 卡片把回复的行内偏移上报给几何登记表，并在折叠/卸载时丢弃。
 */
function source(relative: string): string {
  return readFileSync(fileURLToPath(new URL(relative, import.meta.url)), "utf8");
}

const list = source("../components/issue/timeline-list.tsx");
const card = source("../components/issue/comment-card.tsx");

describe("手机端评论锚点落地接线", () => {
  it("点击引用时把目标评论 id 一并交给 focus 意图", () => {
    expect(list).toContain(
      ".requestFocus(issue.id, outcome.rootId, outcome.commentId)",
    );
  });

  it("targetId 一路传到定位控制器", () => {
    expect(list).toContain("targetId: focusForIssue.targetId,");
  });

  it("先展开所属 root，再启动定位", () => {
    const expandAt = list.indexOf("expandRoot(issue.id, focusForIssue.rootId)");
    const startAt = list.indexOf("locateController.start({");
    expect(expandAt).toBeGreaterThan(-1);
    expect(startAt).toBeGreaterThan(-1);
    // 顺序反了，折叠线程里的回复还没渲染就开始量，必然量不到。
    expect(expandAt).toBeLessThan(startAt);
  });

  it("控制器的行内校正接到真实的列表几何，而不是占位实现", () => {
    expect(list).toContain("geometryRef.current.resolve(targetId,");
    expect(list).toContain("offsetY: scrollGeoRef.current.offsetY,");
    expect(list).toContain("listRef.current?.scrollToOffset({ offset,");
  });

  it("卡片上报回复的行内偏移（气泡基准 + 自身 y）", () => {
    expect(card).toContain("onLayout={(e) => measureReply(reply.id,");
    expect(card).toContain("onLayout={(e) => measureBubble(e.nativeEvent.layout)}");
    expect(card).toContain("offsetInRow: bubbleOffsetRef.current + raw.y,");
  });

  it("折叠或卸载时丢弃测量值，避免按旧布局二次滚动", () => {
    expect(card).toContain("forgetGeometry(id)");
  });
});
