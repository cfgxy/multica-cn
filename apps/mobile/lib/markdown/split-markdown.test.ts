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
