// @vitest-environment node
import { describe, expect, it } from "vitest";
import { AnchorHighlightGate } from "./comment-anchor-highlight";

/**
 * 反例来源（RUYI-108 第三轮 Review 阻断 3）：高亮原本从**点击**起算，
 * 1.7s 的总预算却要和一条最长 4s 的定位路径赛跑——折叠展开、行布局重试、
 * 行内校正各自都能吃掉一秒以上。慢路径下目标最终入屏，高亮却已播完，屏幕上
 * 什么都没被标出来。这组用例把「起算点」钉死在定位流程结束的那一刻。
 */
describe("AnchorHighlightGate", () => {
  it("定位未结束前不交出高亮目标（高亮不得在点击时就起算）", () => {
    const gate = new AnchorHighlightGate();
    gate.arm("reply-9", 7);
    // 尚无任何 settle：调用方拿不到目标，也就无从启动 1.7s 播放。
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
