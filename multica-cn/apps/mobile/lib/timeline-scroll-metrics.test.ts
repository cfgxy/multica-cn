// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  BOTTOM_CHIP_MIN_GAP_PX,
  TOP_CHIP_MIN_GAP_PX,
  assignChipStackSlots,
  computeBottomChipVisible,
  computeJumpChipVisible,
  computeTopChipVisible,
  distFromPhysicalTop,
  distToPhysicalEnd,
  shouldShowBottomChip,
  shouldShowTopChip,
  type ScrollGeometry,
} from "./timeline-scroll-metrics";

describe("timeline-scroll-metrics", () => {
  it("distance is contentHeight - offset - viewport", () => {
    expect(distToPhysicalEnd(1000, 300, 600)).toBe(100);
  });

  it("distance clamps at 0 when overscrolled past the end", () => {
    expect(distToPhysicalEnd(1000, 500, 600)).toBe(0);
  });

  it("chip shows strictly beyond the 48px band and hides inside it", () => {
    expect(shouldShowBottomChip(49)).toBe(true);
    expect(shouldShowBottomChip(48)).toBe(false);
    expect(shouldShowBottomChip(0)).toBe(false);
  });

  it("threshold constant is 48 (approved spec)", () => {
    expect(BOTTOM_CHIP_MIN_GAP_PX).toBe(48);
  });

  it("chip never shows when content is shorter than the viewport", () => {
    // contentHeight 500 < viewport 600 → the user can never scroll away
    // from the physical end; distance semantics must yield "at end".
    expect(distToPhysicalEnd(500, 0, 600)).toBe(0);
    expect(shouldShowBottomChip(distToPhysicalEnd(500, 0, 600))).toBe(false);
  });
});

describe("computeBottomChipVisible", () => {
  it("returns false before the first layout (no geometry yet)", () => {
    const empty: ScrollGeometry = {
      contentHeight: 0,
      offsetY: 0,
      viewportHeight: 0,
    };
    expect(computeBottomChipVisible(empty)).toBe(false);
  });

  it("returns true when saved geometry puts the viewport far from the end", () => {
    const geo: ScrollGeometry = {
      contentHeight: 4000,
      offsetY: 200,
      viewportHeight: 700,
    };
    // dist = 4000 - 900 = 3100 > 48 → visible, even though no scroll event
    // has fired yet (mount / data-growth path).
    expect(computeBottomChipVisible(geo)).toBe(true);
  });

  it("returns false inside the 48px band or when content fits the viewport", () => {
    expect(
      computeBottomChipVisible({
        contentHeight: 700,
        offsetY: 0,
        viewportHeight: 700,
      }),
    ).toBe(false);
    expect(
      computeBottomChipVisible({
        contentHeight: 740,
        offsetY: 0,
        viewportHeight: 700,
      }),
    ).toBe(false);
  });

  it("grows hidden when appended data is already on screen (still at end)", () => {
    // WS append while scrolled to the bottom: content grows, offset is
    // compensated by MVCP so the viewport stays at the physical end.
    const before: ScrollGeometry = {
      contentHeight: 4000,
      offsetY: 3300,
      viewportHeight: 700,
    };
    const after: ScrollGeometry = {
      contentHeight: 4400,
      offsetY: 3700,
      viewportHeight: 700,
    };
    expect(computeBottomChipVisible(before)).toBe(false);
    expect(computeBottomChipVisible(after)).toBe(false);
  });
});

describe("top chip (RUYI-81) — physical scroll-start geometry", () => {
  it("distance from top is the raw offset, clamped at 0", () => {
    expect(distFromPhysicalTop(300)).toBe(300);
    // Overscroll bounce above the first row reports negative offsets on
    // some platforms — the chip must read "at top", not a phantom distance.
    expect(distFromPhysicalTop(-40)).toBe(0);
    expect(distFromPhysicalTop(0)).toBe(0);
  });

  it("chip shows strictly beyond the 48px band and hides inside it", () => {
    expect(shouldShowTopChip(49)).toBe(true);
    expect(shouldShowTopChip(48)).toBe(false);
    expect(shouldShowTopChip(0)).toBe(false);
  });

  it("threshold mirrors the bottom chip's 48px band", () => {
    expect(TOP_CHIP_MIN_GAP_PX).toBe(48);
    expect(TOP_CHIP_MIN_GAP_PX).toBe(BOTTOM_CHIP_MIN_GAP_PX);
  });

  it("computeTopChipVisible returns false before the first layout", () => {
    const empty: ScrollGeometry = {
      contentHeight: 0,
      offsetY: 0,
      viewportHeight: 0,
    };
    expect(computeTopChipVisible(empty)).toBe(false);
  });

  it("computeTopChipVisible is true when scrolled away from the top", () => {
    // offsetY 200 > 48 → the header (title/description/actions) is off
    // screen and the chip should offer the way back.
    expect(
      computeTopChipVisible({
        contentHeight: 4000,
        offsetY: 200,
        viewportHeight: 700,
      }),
    ).toBe(true);
  });

  it("computeTopChipVisible is false at the top or when content fits", () => {
    expect(
      computeTopChipVisible({
        contentHeight: 4000,
        offsetY: 0,
        viewportHeight: 700,
      }),
    ).toBe(false);
    expect(
      computeTopChipVisible({
        contentHeight: 500,
        offsetY: 0,
        viewportHeight: 700,
      }),
    ).toBe(false);
  });
});

describe("assignChipStackSlots (RUYI-81 / RUYI-101)", () => {
  it("只打包底部锚定的两个 chip，to-top 已移出该栈（RUYI-101）", () => {
    expect(assignChipStackSlots({ newChip: true, bottomChip: true })).toEqual({
      newChip: 0,
      bottomChip: 1,
    });
    expect(assignChipStackSlots({ newChip: false, bottomChip: true })).toEqual({
      newChip: -1,
      bottomChip: 0,
    });
  });

  it("gives a lone chip the lowest (thumb-closest) slot", () => {
    expect(assignChipStackSlots({ newChip: false, bottomChip: true })).toEqual({
      newChip: -1,
      bottomChip: 0,
    });
    expect(assignChipStackSlots({ newChip: true, bottomChip: false })).toEqual({
      newChip: 0,
      bottomChip: -1,
    });
  });

  it("never assigns two chips the same slot", () => {
    for (const newChip of [true, false]) {
      for (const bottomChip of [true, false]) {
        const slots = assignChipStackSlots({ newChip, bottomChip });
        const used = [slots.newChip, slots.bottomChip].filter((s) => s >= 0);
        expect(new Set(used).size).toBe(used.length);
      }
    }
  });

  it("hidden chips get -1", () => {
    expect(assignChipStackSlots({ newChip: false, bottomChip: false })).toEqual({
      newChip: -1,
      bottomChip: -1,
    });
  });
});

describe("computeJumpChipVisible (RUYI-101)", () => {
  it("几何与滚动活跃必须同时成立才显示", () => {
    expect(computeJumpChipVisible(true, true)).toBe(true);
    expect(computeJumpChipVisible(true, false)).toBe(false);
    expect(computeJumpChipVisible(false, true)).toBe(false);
    expect(computeJumpChipVisible(false, false)).toBe(false);
  });

  it("位于物理顶部/底部时，即使刚滚动过也不显示对应 chip", () => {
    // 用户一路滚到底：滚动活跃为 true，但到底 chip 的几何条件已不成立。
    const atEnd: ScrollGeometry = {
      contentHeight: 4000,
      offsetY: 3300,
      viewportHeight: 700,
    };
    expect(
      computeJumpChipVisible(computeBottomChipVisible(atEnd), true),
    ).toBe(false);
    // 同理，滚回顶部后到顶 chip 不该驻留。
    const atTop: ScrollGeometry = {
      contentHeight: 4000,
      offsetY: 0,
      viewportHeight: 700,
    };
    expect(computeJumpChipVisible(computeTopChipVisible(atTop), true)).toBe(
      false,
    );
  });

  it("旋转/内容尺寸变化只改几何，不足以让 chip 驻留", () => {
    // 非滚动事件把几何刷成「远离两端」，但没有滚动活跃 → 仍隐藏。
    const mid: ScrollGeometry = {
      contentHeight: 4000,
      offsetY: 1200,
      viewportHeight: 700,
    };
    expect(computeBottomChipVisible(mid)).toBe(true);
    expect(computeTopChipVisible(mid)).toBe(true);
    expect(computeJumpChipVisible(computeBottomChipVisible(mid), false)).toBe(
      false,
    );
    expect(computeJumpChipVisible(computeTopChipVisible(mid), false)).toBe(
      false,
    );
  });
});
