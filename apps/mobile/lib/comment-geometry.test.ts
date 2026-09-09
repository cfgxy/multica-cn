// @vitest-environment node
import { describe, expect, it } from "vitest";
import { CommentGeometryRegistry } from "./comment-geometry";

/** 行顶查表：模拟列表的 `getLayout`，未知行返回 null。 */
const rowsAt = (tops: Record<string, number>) => (rootId: string) =>
  rootId in tops ? tops[rootId] : null;

describe("CommentGeometryRegistry", () => {
  it("把行内偏移换算成内容坐标系的矩形", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    expect(reg.resolve("reply-1", rowsAt({ "root-a": 1200 }))).toEqual({
      top: 1380,
      height: 96,
    });
  });

  it("未登记的评论返回 null（尚未渲染/量到）", () => {
    const reg = new CommentGeometryRegistry();
    expect(reg.resolve("reply-1", rowsAt({ "root-a": 0 }))).toBeNull();
  });

  it("所属行当前无布局时返回 null，而不是按 0 当行顶算", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    // 行被 FlashList 回收 → getLayout 给不出 y。若退化成 0，二次滚动会跳到
    // 列表顶部，用户看到的是「点了跳到不相干的地方」。
    expect(reg.resolve("reply-1", rowsAt({}))).toBeNull();
  });

  it("forget 后不再返回旧坐标", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    reg.forget("reply-1");
    expect(reg.resolve("reply-1", rowsAt({ "root-a": 1200 }))).toBeNull();
  });

  it("重新测量覆盖旧值（行内内容重排后位移）", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 420, height: 300 });
    expect(reg.resolve("reply-1", rowsAt({ "root-a": 1000 }))).toEqual({
      top: 1420,
      height: 300,
    });
  });

  it("同一行内多条回复各自独立换算", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("r1", { rootId: "root-a", offsetInRow: 100, height: 50 });
    reg.report("r2", { rootId: "root-a", offsetInRow: 400, height: 50 });
    const at = rowsAt({ "root-a": 2000 });
    expect(reg.resolve("r1", at)?.top).toBe(2100);
    expect(reg.resolve("r2", at)?.top).toBe(2400);
  });
});
