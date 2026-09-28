// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  removeMentionChip,
  serializeMentionChips,
  tokenAtCursor,
} from "@/lib/mention-serialize";

describe("tokenAtCursor", () => {
  it("行首 @ 命中（query 为空）", () => {
    expect(tokenAtCursor("@", 1)).toEqual({ start: 0, query: "" });
  });

  it("空格后的 @ 命中，query 为 @ 到光标之间的文本", () => {
    expect(tokenAtCursor("hi @world", 9)).toEqual({ start: 3, query: "world" });
  });

  it("换行后的 @ 命中", () => {
    expect(tokenAtCursor("hi\n@", 4)).toEqual({ start: 3, query: "" });
  });

  it("光标落在 query 中间也命中", () => {
    expect(tokenAtCursor("@jo", 2)).toEqual({ start: 0, query: "j" });
  });

  it("邮箱 a@b 不命中（@ 邻接非空白）", () => {
    expect(tokenAtCursor("a@b", 3)).toBeNull();
  });

  it("完整邮箱地址不命中", () => {
    expect(tokenAtCursor("user@example.com", 16)).toBeNull();
  });

  it("query 中含空白则视为已离开 token，不命中", () => {
    expect(tokenAtCursor("hi @jo hn", 9)).toBeNull();
  });

  it("sentinel 开头的已完成 mention 不命中", () => {
    // ⁣ (U+2063) + @Bohan = 已插入的 chip，光标在末尾不应再次触发。
    expect(tokenAtCursor("\u2063@Bohan", 7)).toBeNull();
  });

  it("cursor 越界返回 null", () => {
    expect(tokenAtCursor("hi", 99)).toBeNull();
    expect(tokenAtCursor("hi", 0)).toBeNull();
  });
});

describe("serializeMentionChips", () => {
  it("prepends selected mention links in selection order", () => {
    expect(
      serializeMentionChips("Fix the upload flow", [
        { type: "member", id: "user-1", name: "Bohan" },
        { type: "issue", id: "issue-1", name: "RUYI-133" },
      ]),
    ).toBe(
      "[@Bohan](mention://member/user-1) [RUYI-133](mention://issue/issue-1) Fix the upload flow",
    );
  });

  it("leaves plain text unchanged when no mention chips exist", () => {
    expect(serializeMentionChips("  Keep spacing  ", [])).toBe(
      "  Keep spacing  ",
    );
  });

  it("removes exactly one chip before serializing the remaining mentions", () => {
    const markers = [
      { type: "member" as const, id: "user-1", name: "Bohan" },
      { type: "issue" as const, id: "issue-1", name: "RUYI-133" },
    ];

    const remaining = removeMentionChip(markers, "member", "user-1");

    expect(serializeMentionChips("Fix the upload flow", remaining)).toBe(
      "[RUYI-133](mention://issue/issue-1) Fix the upload flow",
    );
  });
});
