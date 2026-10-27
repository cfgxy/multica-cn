import { describe, expect, it } from "vitest";
import { splitMarkdown } from "./split-markdown";

describe("splitMarkdown — mermaid fences (RUYI-80)", () => {
  it("emits a mermaid segment for a closed ```mermaid fence", () => {
    const chart = "graph LR\n  A[Start] --> B[Done]";
    const input = ["```mermaid", chart, "```"].join("\n");

    expect(splitMarkdown(input)).toEqual([{ type: "mermaid", code: chart }]);
  });

  it("keeps prose around the diagram in order", () => {
    const input = [
      "Before the diagram.",
      "",
      "```mermaid",
      "flowchart TD",
      "  A --> B",
      "```",
      "",
      "After the diagram.",
    ].join("\n");

    const segments = splitMarkdown(input);

    expect(segments).toEqual([
      { type: "prose", content: "Before the diagram." },
      { type: "mermaid", code: "flowchart TD\n  A --> B" },
      { type: "prose", content: "After the diagram." },
    ]);
  });

  it("matches the language as a whole token — `mermaidx` stays plain code", () => {
    // Mirrors web's RichCodeBlock dispatch rule (packages/views/rich-content/
    // rich-code-block.tsx): `language-mermaidx` is an ordinary language, not
    // a diagram. Substring matching here would hijack Shiki code blocks.
    const input = ["```mermaidx", "A --> B", "```"].join("\n");

    expect(splitMarkdown(input)).toEqual([
      { type: "code", lang: "mermaidx", code: "A --> B" },
    ]);
  });

  it("leaves non-mermaid code fences untouched", () => {
    const input = ["```ts", "const x = 1;", "```"].join("\n");

    expect(splitMarkdown(input)).toEqual([
      { type: "code", lang: "ts", code: "const x = 1;" },
    ]);
  });

  it("does not treat a mermaid mention inside prose as a diagram", () => {
    const input = "The word mermaid alone is prose.";

    expect(splitMarkdown(input)).toEqual([
      { type: "prose", content: input },
    ]);
  });
});

/**
 * 表格分段（RUYI-72 手机端）。
 *
 * 表格原本整块交给 enriched 原生渲染，宽表格在手机竖屏下只能横向滚动，
 * 尾部列被藏起来；而 enriched 不允许注入 React（见 split-markdown.ts 顶部
 * 说明），行上挂不了点击手势。拆成独立 segment 后才能自研行详情交互。
 */
describe("splitMarkdown — table segments (RUYI-72)", () => {
  it("把顶层表格拆成独立 segment，并保留表头与各行单元格", () => {
    const segments = splitMarkdown(
      "| 姓名 | 角色 |\n|---|---|\n| 甲 | 开发 |\n| 乙 | 测试 |\n",
    );

    expect(segments).toHaveLength(1);
    const table = segments[0];
    expect(table.type).toBe("table");
    if (table.type !== "table") return;
    expect(table.headers.map((h) => h.text)).toEqual(["姓名", "角色"]);
    expect(table.rows.map((row) => row.map((cell) => cell.text))).toEqual([
      ["甲", "开发"],
      ["乙", "测试"],
    ]);
  });

  it("表格前后的散文各自成段，顺序不变", () => {
    const segments = splitMarkdown("前言\n\n| a |\n|---|\n| 1 |\n\n后记\n");

    expect(segments.map((s) => s.type)).toEqual(["prose", "table", "prose"]);
    expect(segments[0]).toMatchObject({ type: "prose", content: "前言" });
    expect(segments[2]).toMatchObject({ type: "prose", content: "后记" });
  });

  it("单元格保留 inline 结构，链接不会被压成纯文本", () => {
    const segments = splitMarkdown("| 列 |\n|---|\n| [x](https://y) |\n");

    const table = segments[0];
    expect(table.type).toBe("table");
    if (table.type !== "table") return;
    const [[cell]] = table.rows;
    expect(cell.tokens[0]).toMatchObject({ type: "link", href: "https://y" });
  });

  it("没有数据行的表格仍然拆出 segment（表头也要能渲染）", () => {
    const segments = splitMarkdown("| a | b |\n|---|---|\n");

    const table = segments[0];
    expect(table.type).toBe("table");
    if (table.type !== "table") return;
    expect(table.rows).toEqual([]);
  });
});
