// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
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
  // `new URL(relative, import.meta.url)` 会解析成 DOM 的 URL 类型，和
  // `node:url` 的签名对不上（tsc 报 TS2345）。走纯字符串路径拼接绕开。
  return readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), relative),
    "utf8",
  );
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
    expect(list).toContain("geometryRef.current.resolve(");
    expect(list).toContain("offsetY: scrollGeoRef.current.offsetY,");
    expect(list).toContain("listRef.current?.scrollToOffset({ offset,");
  });

  /**
   * 第三轮 Review 阻断 1：`getLayout` 是 item 区坐标，`contentOffset.y` /
   * `scrollToOffset` 是原生滚动坐标，差一个 `getFirstItemOffset()`。判定逻辑
   * 本身在 comment-geometry.test.ts 里覆盖；这里钉住组件真的把这个偏移传下去
   * ——漏掉它，测试全绿而真机上永远滚到 Issue 头部高度那么多的上方。
   */
  it("把 FlashList 的 header 偏移传给几何换算，统一两套坐标", () => {
    expect(list).toContain("listRef.current?.getFirstItemOffset() ?? null");
  });

  it("卡片上报回复的行内偏移（气泡基准 + 自身 y）", () => {
    expect(card).toContain("onLayout={(e) => measureReply(reply.id,");
    expect(card).toContain("onLayout={(e) => measureBubble(e.nativeEvent.layout)}");
    expect(card).toContain("new RowGeometryReporter(");
  });

  /**
   * 第三轮 Review 阻断 2：回复集合变化时旧写法删掉旧集合的全部测量值而不重新
   * 上报。清理必须走差集（syncReplies），整行释放才走 release。
   */
  it("回复集合变化走差集同步，只有折叠/卸载才整行释放", () => {
    expect(card).toContain("reporter.syncReplies(");
    expect(card).toContain("reporter.release()");
    // 旧的「删掉旧集合全部测量值」写法不得复活。
    expect(card).not.toContain("forgetGeometry(id)");
  });

  /**
   * 第四轮 Review 阻断：FlashList v2 回收的是视图——同一个卡片实例会带着另一
   * 个 root 继续渲染，ref 里的上报器活过身份切换。重绑逻辑本身在
   * comment-geometry.test.ts 里覆盖，这里钉住卡片真的按当前 `entry.id` 调用
   * 它，且早于按新集合跑的 `syncReplies`。
   */
  it("卡片在行被复用成另一个 root 时重绑上报器", () => {
    expect(card).toContain("reporter.rebindRoot(entry.id);");
    const rebindAt = card.indexOf("reporter.rebindRoot(entry.id);");
    const syncAt = card.indexOf("reporter.syncReplies(");
    expect(rebindAt).toBeGreaterThan(-1);
    expect(syncAt).toBeGreaterThan(-1);
    // 顺序反了，新集合会先按旧 rootId 登记一轮再被清掉。
    expect(rebindAt).toBeLessThan(syncAt);
    // 必须在 layout effect 里：普通 effect 晚于原生 onLayout 的可能性存在，
    // 那样新行的第一批测量会落到旧 root 名下。
    expect(card).toContain("useLayoutEffect(() => {\n    reporter.rebindRoot(");
  });

  /**
   * 第三轮 Review 阻断 3：高亮必须从**定位流程结束**起算，而不是点击那一刻。
   * 点击处只 arm，起算发生在控制器的结果回调里。
   */
  it("点击只武装高亮，起算发生在定位结果回调里", () => {
    expect(list).toContain("highlightGateRef.current.arm(outcome.commentId,");
    expect(list).toContain("highlightGateRef.current.settle(result.nonce)");
    const armAt = list.indexOf("highlightGateRef.current.arm(");
    const settleAt = list.indexOf("highlightGateRef.current.settle(");
    expect(armAt).toBeGreaterThan(-1);
    expect(settleAt).toBeGreaterThan(-1);
    // 点击处不得再直接把目标写进 highlightedId（那就是点击起算）。
    expect(list).not.toContain("setHighlightedId(outcome.commentId)");
  });
});
