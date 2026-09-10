// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AnchorHighlightGate } from "./comment-anchor-highlight";
import {
  CommentLocateController,
  LOCATE_EXPAND_SETTLE_MS,
  LOCATE_MAX_REFINE_ATTEMPTS,
  LOCATE_REFINE_SETTLE_MS,
} from "./comment-locate";
import type { TargetRect } from "./comment-target-visibility";

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

/**
 * 反例来源（RUYI-108 第三轮 Review 阻断 3）：高亮原本从**点击**起算，
 * 1.7s 的总预算却要和一条最长 4s 的定位路径赛跑——折叠展开、行布局重试、
 * 行内校正各自都能吃掉一秒以上。慢路径下目标最终入屏，高亮却已播完，屏幕上
 * 什么都没被标出来。这组用例把「起算点」钉死在定位流程结束的那一刻。
 */
describe("AnchorHighlightGate", () => {
  it("别的定位（旧 nonce）结束时不交出本次目标", () => {
    const gate = new AnchorHighlightGate();
    gate.arm("reply-9", 7);
    expect(gate.settle(6)).toBeNull();
  });

  it("定位成功时交出目标，高亮此刻才开始完整播放", () => {
    const gate = new AnchorHighlightGate();
    gate.arm("reply-9", 7);
    expect(gate.settle(7)).toBe("reply-9");
  });

  it("定位失败同样放行——目标可能本来就在屏上，静默等于点击没反应", () => {
    const gate = new AnchorHighlightGate();
    gate.arm("reply-9", 7);
    expect(gate.settle(7)).toBe("reply-9");
  });

  it("同一次定位只交出一次，迟到的重复回调不再重启高亮", () => {
    const gate = new AnchorHighlightGate();
    gate.arm("reply-9", 7);
    expect(gate.settle(7)).toBe("reply-9");
    expect(gate.settle(7)).toBeNull();
  });

  it("连点两个引用时，前一次定位的迟到回调不得点亮后一次的目标", () => {
    const gate = new AnchorHighlightGate();
    gate.arm("reply-9", 7);
    gate.arm("reply-42", 8);
    // nonce 7 的定位结果姗姗来迟，此时武装的是 nonce 8。
    expect(gate.settle(7)).toBeNull();
    expect(gate.settle(8)).toBe("reply-42");
  });

  it("disarm 后不再交出目标（时间线卸载）", () => {
    const gate = new AnchorHighlightGate();
    gate.arm("reply-9", 7);
    gate.disarm();
    expect(gate.settle(7)).toBeNull();
  });
});

/**
 * 组合逻辑反例（第四轮 REVIEW NOTE）。
 *
 * 上面那组只覆盖闸门自身的状态机；「点击时不起算、定位结束才起算」这条产品
 * 语义靠的是闸门与定位控制器**接在一起**的时序。这里把真实的
 * `CommentLocateController` 接上闸门（时间线组件里就是这么接的：点击 `arm`，
 * 结果回调 `settle`），用虚拟时钟走完慢路径，断言高亮起算的时刻——而不是像
 * 接线测试那样只断言源码里出现了某个调用。
 */
describe("AnchorHighlightGate × CommentLocateController 组合时序", () => {
  const REQ = { issueId: "i1", rootId: "r1", targetId: "reply-9", nonce: 7 };

  /** 只暴露组合所需的最小面：行落位可控、目标矩形可控。 */
  function makeRig(targetRect: TargetRect | null) {
    const gate = new AnchorHighlightGate();
    /** 高亮实际起算的时刻（虚拟时钟毫秒）；null = 还没起算。 */
    let highlightStartedAt: number | null = null;
    let highlighted: string | null = null;
    const viewable = new Set<string>();
    let rect = targetRect;
    let resolveScroll!: () => void;
    const controller = new CommentLocateController(
      {
        findIndex: () => 3,
        getLayout: () => ({ y: 120 }),
        scrollToIndex: () =>
          new Promise<void>((res) => {
            resolveScroll = res;
          }),
        isViewable: (rootId) => viewable.has(rootId),
        measureTarget: () => rect,
        getViewport: () => ({ offsetY: 0, height: 800 }),
        scrollToOffset: () => {},
        schedule: (fn, ms) => {
          const id = setTimeout(fn, ms);
          return { cancel: () => clearTimeout(id) };
        },
      },
      (result) => {
        // 时间线组件里的那一行：结果回调才 settle，settle 出目标才起算高亮。
        const pending = gate.settle(result.nonce);
        if (pending) {
          highlighted = pending;
          highlightStartedAt = Date.now();
        }
      },
    );
    return {
      gate,
      controller,
      viewable,
      setRect: (r: TargetRect | null) => (rect = r),
      settleScroll: () => resolveScroll(),
      get highlighted() {
        return highlighted;
      },
      get highlightStartedAt() {
        return highlightStartedAt;
      },
    };
  }

  it("慢定位路径下高亮不在点击时起算，而在目标入屏那一刻起算", async () => {
    vi.setSystemTime(0);
    // 目标一开始量不到（折叠线程刚展开、行内 markdown 还在排版）。
    const rig = makeRig(null);

    // 点击：只武装。
    rig.gate.arm("reply-9", 7);
    rig.controller.start(REQ);
    expect(rig.highlighted).toBeNull();

    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    rig.viewable.add("r1");
    rig.settleScroll();
    await vi.advanceTimersByTimeAsync(0);
    // 行已落位、行级 viewability 也有了，但目标还没量到 → 仍未起算。
    expect(rig.highlighted).toBeNull();

    // 走满一轮校正等待，仍未量到。
    await vi.advanceTimersByTimeAsync(LOCATE_REFINE_SETTLE_MS);
    expect(rig.highlighted).toBeNull();

    // 排版完成、目标已在视口内 → 下一轮校正判定成功。
    rig.setRect({ top: 200, height: 100 });
    await vi.advanceTimersByTimeAsync(LOCATE_REFINE_SETTLE_MS);
    expect(rig.highlighted).toBe("reply-9");
    // 起算点晚于点击（0），1.7s 的播放窗口从这里才开始，不与定位赛跑。
    expect(rig.highlightStartedAt).toBeGreaterThan(0);
  });

  it("定位失败同样起算高亮——目标可能一直就在屏上", async () => {
    vi.setSystemTime(0);
    const rig = makeRig(null); // 永远量不到 → 校正耗尽后 failed(layout)
    rig.gate.arm("reply-9", 7);
    rig.controller.start(REQ);
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    rig.viewable.add("r1");
    rig.settleScroll();
    await vi.advanceTimersByTimeAsync(0);
    expect(rig.highlighted).toBeNull();

    await vi.advanceTimersByTimeAsync(
      LOCATE_REFINE_SETTLE_MS * (LOCATE_MAX_REFINE_ATTEMPTS + 1),
    );
    expect(rig.highlighted).toBe("reply-9");
  });

  it("连点两个引用时，前一次定位的迟到结果不点亮后一次的目标", async () => {
    vi.setSystemTime(0);
    const rig = makeRig({ top: 200, height: 100 });
    // 第一次点击武装 nonce 7，随后用户立刻点了另一条引用（nonce 8）。
    rig.gate.arm("reply-9", 7);
    rig.controller.start(REQ);
    rig.gate.arm("reply-42", 8);

    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    rig.viewable.add("r1");
    rig.settleScroll();
    await vi.advanceTimersByTimeAsync(0);

    // nonce 7 的结果到了，但武装的是 nonce 8 → 不得点亮 reply-9。
    expect(rig.highlighted).toBeNull();
  });
});
