import { describe, expect, it } from "vitest";
import { promoteSoftBreaks } from "./soft-breaks";
import { preprocessMobileMarkdown } from "./preprocess";
import { splitMarkdown } from "./split-markdown";

/**
 * RUYI-235 parity contract: web renders a paragraph-internal single `\n` as a
 * line break (remark-breaks in packages/ui/markdown/Markdown.tsx); mobile's
 * native renderer (md4c via react-native-enriched-markdown) follows CommonMark
 * and folds it into a space, so multi-line comments and numbered agent output
 * collapse into one line. `promoteSoftBreaks` upgrades those soft breaks to
 * the canonical hard-break form (`"  \n"`) in the string domain, before the
 * enriched renderer sees them.
 */
describe("promoteSoftBreaks", () => {
  it("promotes a single \\n between two text lines to the hard-break form", () => {
    expect(promoteSoftBreaks("第一行\n第二行")).toBe("第一行  \n第二行");
  });

  it("promotes every paragraph-internal newline in numbered agent output", () => {
    const input = "1. 先确认账号状态\n2. 重新登录\n3. 如仍失败联系管理员";
    expect(promoteSoftBreaks(input)).toBe(
      "1. 先确认账号状态  \n2. 重新登录  \n3. 如仍失败联系管理员",
    );
  });

  it("promotes continuation lines inside a list item", () => {
    expect(promoteSoftBreaks("- 待办一\n  继续说明")).toBe(
      "- 待办一  \n  继续说明",
    );
  });

  it("leaves blank-line paragraph separators untouched", () => {
    expect(promoteSoftBreaks("段落一\n\n段落二")).toBe("段落一\n\n段落二");
  });

  it("leaves blank-line runs (3+ newlines) untouched", () => {
    expect(promoteSoftBreaks("a\n\n\nb")).toBe("a\n\n\nb");
  });

  it("leaves whitespace-only blank separators untouched", () => {
    expect(promoteSoftBreaks("a\n   \nb")).toBe("a\n   \nb");
  });

  it("does not touch fenced code blocks or their adjacent separators", () => {
    const input = ["说明如下", "```js", "const a = 1;", "const b = 2;", "```", "后续说明"].join("\n");
    expect(promoteSoftBreaks(input)).toBe(input);
  });

  it("leaves fenced code untouched when embedded in a list (splitter keeps it in prose)", () => {
    const input = ["- 步骤一", "  ```bash", "  git status", "  ```", "- 步骤二"].join("\n");
    expect(promoteSoftBreaks(input)).toBe(input);
  });

  it("does not double-promote lines that already end with a hard break", () => {
    expect(promoteSoftBreaks("第一行  \n第二行")).toBe("第一行  \n第二行");
  });

  it("is idempotent", () => {
    const once = promoteSoftBreaks("第一行\n第二行\n\n第三行");
    expect(promoteSoftBreaks(once)).toBe(once);
  });

  it("returns input without newlines untouched", () => {
    expect(promoteSoftBreaks("单行内容")).toBe("单行内容");
  });

  it("keeps working after the <br> → \"  \\n\" pass in preprocess", () => {
    const processed = preprocessMobileMarkdown("第一行<br>第二行");
    expect(promoteSoftBreaks(processed)).toBe("第一行  \n第二行");
  });
});

describe("soft-break promotion wired into the split pipeline", () => {
  it("prose segments get hard breaks; code segments stay byte-clean", () => {
    const content = [
      "排查结论：",
      "1. 账号被锁定",
      "2. 需要重置密码",
      "",
      "```bash",
      "git status",
      "```",
      "完成。",
    ].join("\n");

    const segments = splitMarkdown(preprocessMobileMarkdown(content));

    const prose = segments.filter((s) => s.type === "prose");
    expect(prose[0]).toMatchObject({
      type: "prose",
      content: "排查结论：  \n1. 账号被锁定  \n2. 需要重置密码",
    });
    expect(prose[1]).toMatchObject({ type: "prose", content: "完成。" });

    const code = segments.find((s) => s.type === "code");
    expect(code).toMatchObject({ type: "code", lang: "bash", code: "git status" });
  });
});
