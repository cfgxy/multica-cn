import { describe, expect, it } from "vitest";

import { appendVoiceTurnToDraft } from "./draft-backfill";

/**
 * lib/voice/draft-backfill.ts 单测（RUYI-474）。Issue 评论草稿的语音回填
 * 拼接不变量：
 *  1. 空草稿 → 口述内容直接成为草稿。
 *  2. 非空草稿 → 空行分隔追加，多段口述互不粘连。
 *  3. 草稿尾部空白先收敛，追加后不产生三连空行。
 */
describe("appendVoiceTurnToDraft", () => {
  it("空草稿直接落入口述内容", () => {
    expect(appendVoiceTurnToDraft("", "hello from voice")).toBe(
      "hello from voice",
    );
  });

  it("非空草稿以空行分隔追加", () => {
    expect(appendVoiceTurnToDraft("typed first", "then spoken")).toBe(
      "typed first\n\nthen spoken",
    );
  });

  it("草稿尾部空白先收敛再拼接", () => {
    expect(appendVoiceTurnToDraft("typed first  \n", "then spoken")).toBe(
      "typed first\n\nthen spoken",
    );
  });

  it("口述内容保持原样（含内部换行）", () => {
    expect(appendVoiceTurnToDraft("", "line one\nline two")).toBe(
      "line one\nline two",
    );
  });
});
