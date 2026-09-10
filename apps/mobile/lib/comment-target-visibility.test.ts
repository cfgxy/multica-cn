// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  TARGET_MIN_VISIBLE_PX,
  TARGET_VIEW_OFFSET_PX,
  isTargetVisible,
  targetScrollOffset,
} from "./comment-target-visibility";

const VP = { offsetY: 1000, height: 800 }; // 视口覆盖 1000–1800

describe("isTargetVisible", () => {
  it("目标顶边在视口内且下方露出足够内容 → 可见", () => {
    expect(isTargetVisible({ top: 1100, height: 200 }, VP)).toBe(true);
  });

  it("目标整个落在视口下方 → 不可见（长线程里 root 行可见的典型失效点）", () => {
    expect(isTargetVisible({ top: 2400, height: 200 }, VP)).toBe(false);
  });

  it("目标整个落在视口上方 → 不可见", () => {
    expect(isTargetVisible({ top: 200, height: 100 }, VP)).toBe(false);
  });

  it("只在视口下沿露出一条缝 → 不算可见", () => {
    // 顶边在视口内，但下方只剩几像素，用户读不到任何内容。
    expect(
      isTargetVisible({ top: VP.offsetY + VP.height - 6, height: 400 }, VP),
    ).toBe(false);
  });

  it("恰好露出最小可读高度 → 可见", () => {
    expect(
      isTargetVisible(
        { top: VP.offsetY + VP.height - TARGET_MIN_VISIBLE_PX, height: 400 },
        VP,
      ),
    ).toBe(true);
  });

  it("目标比最小可读高度还矮时，只要求它自己完整露出", () => {
    // 一条两行的短回复（高 12px）不该因为「露不满 24px」而永远判不成功。
    const shortAtBottom = { top: VP.offsetY + VP.height - 12, height: 12 };
    expect(isTargetVisible(shortAtBottom, VP)).toBe(true);
  });

  it("目标比视口还高时，顶边入屏即算到位", () => {
    expect(isTargetVisible({ top: 1010, height: 5000 }, VP)).toBe(true);
  });

  it("视口高度未知（尚未布局）时判不可见，避免据空几何提前收工", () => {
    expect(isTargetVisible({ top: 0, height: 100 }, { offsetY: 0, height: 0 })).toBe(
      false,
    );
  });

  it("顶边略高于视口顶部但在容差内 → 仍算可见（滚动落点是估算值）", () => {
    expect(isTargetVisible({ top: VP.offsetY - 3, height: 300 }, VP)).toBe(true);
  });
});

describe("targetScrollOffset", () => {
  it("把目标顶边放到视口靠上，留出落点空隙", () => {
    expect(targetScrollOffset({ top: 1500, height: 100 })).toBe(
      1500 - TARGET_VIEW_OFFSET_PX,
    );
  });

  it("靠近内容顶部的目标不会要求负偏移", () => {
    expect(targetScrollOffset({ top: 3, height: 100 })).toBe(0);
  });
});
