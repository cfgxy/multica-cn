// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import {
  CommentGeometryRegistry,
  RowGeometryReporter,
} from "./comment-geometry";

/** 行顶查表：模拟列表的 `getLayout`（item 区坐标），未知行返回 null。 */
const rowsAt = (tops: Record<string, number>) => (rootId: string) =>
  rootId in tops ? tops[rootId] : null;

/** 无 ListHeader 的场景：item 区坐标即原生滚动坐标。 */
const NO_HEADER = 0;

describe("CommentGeometryRegistry", () => {
  it("把行内偏移换算成内容坐标系的矩形", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    expect(
      reg.resolve("reply-1", rowsAt({ "root-a": 1200 }), NO_HEADER),
    ).toEqual({ top: 1380, height: 96 });
  });

  it("未登记的评论返回 null（尚未渲染/量到）", () => {
    const reg = new CommentGeometryRegistry();
    expect(reg.resolve("reply-1", rowsAt({ "root-a": 0 }), NO_HEADER)).toBeNull();
  });

  it("所属行当前无布局时返回 null，而不是按 0 当行顶算", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    // 行被 FlashList 回收 → getLayout 给不出 y。若退化成 0，二次滚动会跳到
    // 列表顶部，用户看到的是「点了跳到不相干的地方」。
    expect(reg.resolve("reply-1", rowsAt({}), NO_HEADER)).toBeNull();
  });

  it("forget 后不再返回旧坐标", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    reg.forget("reply-1");
    expect(
      reg.resolve("reply-1", rowsAt({ "root-a": 1200 }), NO_HEADER),
    ).toBeNull();
  });

  it("重新测量覆盖旧值（行内内容重排后位移）", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 420, height: 300 });
    expect(
      reg.resolve("reply-1", rowsAt({ "root-a": 1000 }), NO_HEADER),
    ).toEqual({ top: 1420, height: 300 });
  });

  it("同一行内多条回复各自独立换算", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("r1", { rootId: "root-a", offsetInRow: 100, height: 50 });
    reg.report("r2", { rootId: "root-a", offsetInRow: 400, height: 50 });
    const at = rowsAt({ "root-a": 2000 });
    expect(reg.resolve("r1", at, NO_HEADER)?.top).toBe(2100);
    expect(reg.resolve("r2", at, NO_HEADER)?.top).toBe(2400);
  });

  /**
   * 反例来源（第三轮 Review 阻断 1）：FlashList 的 `getLayout(i).y` 是
   * **item 区**坐标，而 `onScroll` 给的 `contentOffset.y` 和
   * `scrollToOffset`（默认 `skipFirstItemOffset: true`）走的是**原生**滚动
   * 坐标，两者相差一个 `getFirstItemOffset()`——也就是 ListHeaderComponent
   * 的高度加顶部内边距。Issue 头部（标题、描述、反应行）在真机上轻松几百
   * 像素，少算这一段会让二次滚动始终停在目标上方，反复校正直到判失败。
   */
  it("把 ListHeader 偏移计入内容坐标（否则始终滚到目标上方）", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    const headerHeight = 420;
    expect(
      reg.resolve("reply-1", rowsAt({ "root-a": 1200 }), headerHeight),
    ).toEqual({ top: 1200 + 180 + 420, height: 96 });
  });

  it("列表未挂载（拿不到 header 偏移）时返回 null，而不是按 0 当没有头部", () => {
    const reg = new CommentGeometryRegistry();
    reg.report("reply-1", { rootId: "root-a", offsetInRow: 180, height: 96 });
    expect(
      reg.resolve("reply-1", rowsAt({ "root-a": 1200 }), null),
    ).toBeNull();
  });
});

/**
 * 反例来源（第三轮 Review 阻断 2）：卡片侧原本用「`replyIds` 变化 →
 * 旧 effect cleanup 删掉旧集合的全部测量值」来清理几何，而新 effect 并不
 * 重新 flush。线程新增一条回复后，仍然挂载的老回复不保证再触发 `onLayout`，
 * 它们的测量值就此永久消失，控制器只能空转到有界重试耗尽后判失败。
 *
 * 正确的语义是差集：只丢弃**真正消失**的回复，仍在的一律保留并按当前基准
 * 重新登记。
 */
describe("RowGeometryReporter", () => {
  function makeReporter(rootId = "root-a") {
    const report = vi.fn();
    const forget = vi.fn();
    return {
      report,
      forget,
      reporter: new RowGeometryReporter(rootId, report, forget),
    };
  }

  it("回复集合新增成员后，已挂载回复的测量值仍然有效", () => {
    const { reporter, report, forget } = makeReporter();
    reporter.setBubbleOffset(40);
    reporter.measureReply("r1", { y: 100, height: 50 });
    reporter.syncReplies(["r1"]);
    report.mockClear();
    forget.mockClear();

    // 线程新增 r2：r1 仍然挂载，且不保证再触发 onLayout。
    reporter.syncReplies(["r1", "r2"]);

    expect(forget).not.toHaveBeenCalled();
    expect(report).toHaveBeenCalledWith("r1", {
      rootId: "root-a",
      offsetInRow: 140,
      height: 50,
    });
  });

  it("回复真的消失时才丢弃其测量值", () => {
    const { reporter, report, forget } = makeReporter();
    reporter.setBubbleOffset(0);
    reporter.measureReply("r1", { y: 100, height: 50 });
    reporter.measureReply("r2", { y: 200, height: 50 });
    reporter.syncReplies(["r1", "r2"]);
    report.mockClear();
    forget.mockClear();

    reporter.syncReplies(["r1"]);

    expect(forget).toHaveBeenCalledTimes(1);
    expect(forget).toHaveBeenCalledWith("r2");
    expect(report).toHaveBeenCalledWith("r1", {
      rootId: "root-a",
      offsetInRow: 100,
      height: 50,
    });
  });

  it("气泡基准迟到时按新基准重算全部已登记回复", () => {
    const { reporter, report } = makeReporter();
    // RN 里子节点的 onLayout 通常先于父节点，基准来得比回复晚。
    reporter.measureReply("r1", { y: 100, height: 50 });
    report.mockClear();
    reporter.setBubbleOffset(40);
    expect(report).toHaveBeenCalledWith("r1", {
      rootId: "root-a",
      offsetInRow: 140,
      height: 50,
    });
  });

  it("release 丢弃全部测量值（折叠 / 行被回收）", () => {
    const { reporter, forget } = makeReporter();
    reporter.setBubbleOffset(0);
    reporter.measureReply("r1", { y: 100, height: 50 });
    reporter.measureReply("r2", { y: 200, height: 50 });
    reporter.release();
    expect(forget.mock.calls.map((c) => c[0]).sort()).toEqual(["r1", "r2"]);
  });

  it("release 之后重新测量仍然工作（行回收后再次进入渲染窗口）", () => {
    const { reporter, report } = makeReporter();
    reporter.setBubbleOffset(40);
    reporter.measureReply("r1", { y: 100, height: 50 });
    reporter.release();
    report.mockClear();
    reporter.measureReply("r1", { y: 120, height: 50 });
    expect(report).toHaveBeenCalledWith("r1", {
      rootId: "root-a",
      offsetInRow: 160,
      height: 50,
    });
  });
});

/**
 * 反例来源（第四轮 Review 阻断）：FlashList v2 是 **view recycling**——
 * 行滚出渲染窗口时组件实例不卸载，而是带着新 item 复用（`RenderStackManager`
 * 把回收的 key 分配给新的 stableId）。卡片侧的上报器存在 ref 里、构造时就把
 * `rootId` 定死，因此同一个实例会从 `root-a` 被复用成 `root-b`：新 root 的
 * 回复被登记到**旧 root 名下**，`resolve` 拿旧行的行顶去换算，目标滚到错误
 * 线程；旧 root 的测量值也没人清，行不在渲染窗口里时 `getLayout` 给 null，
 * 定位只能空转到重试耗尽。
 *
 * 正确语义：行身份变化时先释放旧 root 的全部测量，再把上报器绑到当前 root。
 */
describe("RowGeometryReporter 跨 root 回收", () => {
  it("行被复用成另一个 root 时释放旧测量，新回复登记到新 root", () => {
    const report = vi.fn();
    const forget = vi.fn();
    const reporter = new RowGeometryReporter("root-a", report, forget);
    reporter.setBubbleOffset(40);
    reporter.measureReply("r1", { y: 100, height: 50 });
    report.mockClear();
    forget.mockClear();

    // FlashList 把这个 cell 复用给了 root-b。
    reporter.rebindRoot("root-b");

    expect(forget).toHaveBeenCalledTimes(1);
    expect(forget).toHaveBeenCalledWith("r1");
    // 旧基准同样作废：新行的气泡偏移要重新量，不能拿旧行的 40 顶上。
    reporter.measureReply("r2", { y: 100, height: 50 });
    expect(report).toHaveBeenCalledWith("r2", {
      rootId: "root-b",
      offsetInRow: 100,
      height: 50,
    });
    expect(report).not.toHaveBeenCalledWith(
      "r2",
      expect.objectContaining({ rootId: "root-a" }),
    );
  });

  it("行身份未变时 rebindRoot 是空操作，不误删仍有效的几何", () => {
    const report = vi.fn();
    const forget = vi.fn();
    const reporter = new RowGeometryReporter("root-a", report, forget);
    reporter.setBubbleOffset(40);
    reporter.measureReply("r1", { y: 100, height: 50 });
    report.mockClear();
    forget.mockClear();

    // 每次渲染都会调用（同一行的普通重渲染远多于回收）。
    reporter.rebindRoot("root-a");
    reporter.rebindRoot("root-a");

    expect(forget).not.toHaveBeenCalled();
    reporter.setBubbleOffset(40);
    expect(report).toHaveBeenCalledWith("r1", {
      rootId: "root-a",
      offsetInRow: 140,
      height: 50,
    });
  });

  it("回收后登记表按新 root 解析，旧回复不再返回坐标", () => {
    const reg = new CommentGeometryRegistry();
    const reporter = new RowGeometryReporter(
      "root-a",
      (id, rect) => reg.report(id, rect),
      (id) => reg.forget(id),
    );
    reporter.setBubbleOffset(40);
    reporter.measureReply("r1", { y: 100, height: 50 });
    expect(reg.resolve("r1", rowsAt({ "root-a": 1200 }), NO_HEADER)).toEqual({
      top: 1340,
      height: 50,
    });

    reporter.rebindRoot("root-b");
    reporter.setBubbleOffset(20);
    reporter.measureReply("r2", { y: 60, height: 50 });

    // 旧 root 的回复已被清除：控制器据此走有界重试，而不是拿旧坐标乱滚。
    expect(reg.resolve("r1", rowsAt({ "root-a": 1200 }), NO_HEADER)).toBeNull();
    // 新 root 的回复按新行的行顶换算。
    expect(reg.resolve("r2", rowsAt({ "root-b": 300 }), NO_HEADER)).toEqual({
      top: 380,
      height: 50,
    });
  });
});
