// @vitest-environment node
import { describe, expect, it } from "vitest";
import { mentionTriggerFromInput } from "@/lib/mention-trigger";

/**
 * RUYI-232 触发谓词：只有「本次输入新增了 @，且该 @ 前是空白或行首」
 * 才允许弹出 @ 选择器。邮箱 `a@b` 类邻接文本不得触发。
 */
describe("mentionTriggerFromInput", () => {
  it("行首输入 @ 触发", () => {
    expect(mentionTriggerFromInput("", "@", 0)).toEqual({
      start: 0,
      query: "",
    });
  });

  it("空格后输入 @ 触发", () => {
    expect(mentionTriggerFromInput("hi ", "hi @", 3)).toEqual({
      start: 3,
      query: "",
    });
  });

  it("换行后输入 @ 触发", () => {
    expect(mentionTriggerFromInput("hi\n", "hi\n@", 3)).toEqual({
      start: 3,
      query: "",
    });
  });

  it("邮箱形态 a@b 不触发（@ 邻接非空白）", () => {
    expect(mentionTriggerFromInput("a", "a@", 1)).toBeNull();
  });

  it("邮箱地址中途补打 @ 不触发", () => {
    // user 已有，光标在 user 后补 @ → "user@" 的 @ 邻接 'r'。
    expect(mentionTriggerFromInput("user", "user@", 4)).toBeNull();
  });

  it("已有邮箱地址后追加字符不触发", () => {
    expect(mentionTriggerFromInput("a@", "a@b", 2)).toBeNull();
    expect(mentionTriggerFromInput("a@b", "a@bc", 3)).toBeNull();
  });

  it("token 内继续打字不重复触发", () => {
    expect(mentionTriggerFromInput("hi @", "hi @j", 4)).toBeNull();
    expect(mentionTriggerFromInput("hi @jo", "hi @john", 6)).toBeNull();
  });

  it("纯删除不触发", () => {
    expect(mentionTriggerFromInput("hi @", "hi ", 3)).toBeNull();
  });

  it("等长替换（新增段为空）不触发", () => {
    expect(mentionTriggerFromInput("hi @", "hi ?", 4)).toBeNull();
  });

  it("新增字符不含 @ 时不触发", () => {
    expect(mentionTriggerFromInput("hi", "hi bo", 2)).toBeNull();
  });

  it("粘贴「空白前缀 + @token」按谓词触发（@ 前是空白）", () => {
    expect(mentionTriggerFromInput("hi", "hi @bo", 2)).toEqual({
      start: 3,
      query: "bo",
    });
  });

  it("光标在文本中间、其后再有旧 @ 时插入新字符不触发", () => {
    // "hi @j|ohn"（cursor=5）插 "x" → "hi @jxohn"：新增段无 @。
    expect(mentionTriggerFromInput("hi @john", "hi @jxohn", 5)).toBeNull();
  });

  it("在非空白前插入 @ 不触发（谓词由 tokenAtCursor 兜底）", () => {
    // "a|@b"（cursor=1）再打一个 @ → "a@@b"：新 @ 邻接 'a'。
    expect(mentionTriggerFromInput("a@b", "a@@b", 1)).toBeNull();
  });
});
