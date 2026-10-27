// @vitest-environment node
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  COMMENT_HIGHLIGHT_FADE_MS,
  COMMENT_HIGHLIGHT_HOLD_MS,
} from "@multica/core/issues/comment-highlight";

/**
 * Web/Desktop 高亮时长与共享时间表的一致性（RUYI-108）。
 *
 * 淡出时长是 Tailwind 类名 `duration-300`，而 Tailwind 只编译源码里字面出现的
 * 类，因此 `comment-card.tsx` 无法插值 `COMMENT_HIGHLIGHT_FADE_MS`。唯一现实的
 * 失配路径就是有人只改了一边——高亮总时长随之偏离 1–2 秒，而任何 DOM 断言都看
 * 不出来（jsdom 不跑 CSS 过渡）。所以这里按源码断言，而不是挂组件。
 *
 * 清除高亮的定时器（issue-detail.tsx）本身用的是常量，不是字面量，故只断言它
 * 不再有写死的毫秒数。
 */

function source(relative: string): string {
  return readFileSync(
    fileURLToPath(new URL(relative, import.meta.url)),
    "utf8",
  );
}

describe("评论高亮时长跨端一致", () => {
  it("comment-card 里所有高亮过渡都等于 FADE 常量", () => {
    const durations = new Set(
      [...source("./comment-card.tsx").matchAll(/transition-colors duration-(\d+)/g)]
        .map((m) => Number(m[1])),
    );

    // 前提断言：类名形态变了（比如换成 CSS 变量）时要显式失败，而不是空集通过。
    expect(durations.size).toBeGreaterThan(0);
    expect([...durations]).toEqual([COMMENT_HIGHLIGHT_FADE_MS]);
  });

  it("issue-detail 的高亮清除定时器取自共享常量，不写死毫秒", () => {
    const detail = source("./issue-detail.tsx");

    expect(detail).toContain(
      "setHighlightedId(null),\n      COMMENT_HIGHLIGHT_HOLD_MS,",
    );
    // 旧值曾是 2500，且与 700ms 过渡叠加到 3.2s。任何写死的毫秒重新出现在这个
    // 调用里都会绕开共享时间表。
    expect(detail).not.toMatch(/setHighlightedId\(null\),\s*\d+\)/);
  });

  it("保持窗口本身仍在 1 秒以上，避免落地滚动还没停就闪完", () => {
    expect(COMMENT_HIGHLIGHT_HOLD_MS).toBeGreaterThan(1000);
  });
});
