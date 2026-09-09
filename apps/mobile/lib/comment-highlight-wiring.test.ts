// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  COMMENT_HIGHLIGHT_FADE_MS,
  COMMENT_HIGHLIGHT_TOTAL_MS,
} from "@multica/core/issues/comment-highlight";

/**
 * 手机端高亮时长接线（RUYI-108）。
 *
 * 手机端的高亮由两处协作：`comment-card` 里 Reanimated 的
 * fade-in → hold → fade-out 序列负责画，`timeline-list` 的定时器负责在到点时
 * 把 `highlightedId` 置空。置空会直接卸掉 overlay，所以这个闸门必须覆盖
 * **整段** `TOTAL`；写成 `HOLD` 会在淡出刚开始时把 overlay 抽走，动画不是淡出
 * 而是瞬灭——看得见，却测不出来（Reanimated 在 node lane 里跑不起来，组件也
 * 依赖 RN 原生模块）。故按源码断言，这是本仓能观察到该失配的唯一层。
 */
function source(relative: string): string {
  // `new URL(relative, import.meta.url)` 会解析成 DOM 的 URL 类型，和
  // `node:url` 的签名对不上（tsc 报 TS2345）。走纯字符串路径拼接绕开。
  return readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), relative),
    "utf8",
  );
}

describe("手机端评论高亮时长", () => {
  it("timeline-list 的清除闸门覆盖整段可见时长，而不是只覆盖保持段", () => {
    const list = source("../components/issue/timeline-list.tsx");

    expect(list).toContain(
      "const HIGHLIGHT_HOLD_MS = COMMENT_HIGHLIGHT_TOTAL_MS;",
    );
    // 前提断言：闸门仍由这个常量驱动。改名或改成字面量都要显式失败。
    expect(list).toContain("setHighlightedId(null), HIGHLIGHT_HOLD_MS)");
  });

  it("comment-card 的两段 overlay 动画都取自共享时间表，不写死毫秒", () => {
    const card = source("../components/issue/comment-card.tsx");

    const sequences = [
      ...card.matchAll(/withTiming\(\s*[01]\s*,\s*\{\s*duration:\s*([^\s}]+)/g),
    ].map((m) => m[1]);

    // root 与 reply 两个 overlay，各自 fade-in / fade-out 共 4 处。
    expect(sequences).toHaveLength(4);
    expect(new Set(sequences)).toEqual(new Set(["COMMENT_HIGHLIGHT_FADE_MS"]));

    const holds = [...card.matchAll(/withDelay\(\s*([^,]+),/g)].map((m) =>
      m[1].trim(),
    );
    expect(holds).toEqual([
      "COMMENT_HIGHLIGHT_TOTAL_MS - 2 * COMMENT_HIGHLIGHT_FADE_MS",
      "COMMENT_HIGHLIGHT_TOTAL_MS - 2 * COMMENT_HIGHLIGHT_FADE_MS",
    ]);
    // 保持段必须为正，否则 Reanimated 会跳过它，闪一下就没了。
    expect(COMMENT_HIGHLIGHT_TOTAL_MS - 2 * COMMENT_HIGHLIGHT_FADE_MS).toBeGreaterThan(0);
  });
});
