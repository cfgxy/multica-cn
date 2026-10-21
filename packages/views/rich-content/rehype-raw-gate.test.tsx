// @vitest-environment node
import ReactMarkdown, { type Options as ReactMarkdownOptions } from "react-markdown";
import { renderToStaticMarkup } from "react-dom/server";
import type { Root } from "hast";
import rehypeKatex from "rehype-katex";
import rehypeRaw from "rehype-raw";
import rehypeSanitize from "rehype-sanitize";
import remarkBreaks from "remark-breaks";
import remarkCjkFriendly from "remark-cjk-friendly/parseOnly";
import remarkMath from "remark-math";
import { markdownSanitizeSchema } from "@multica/ui/markdown";
import { describe, expect, it } from "vitest";
import { remarkRepairCjkStrongTrailingWhitespace } from "./cjk-emphasis";
import {
  rehypeKatexWhenNeeded,
  rehypeRawWhenNeeded,
} from "./rehype-raw-gate";
import { remarkGfmWithSharedExtensions } from "./shared-gfm-extensions";
import { remarkRestoreCurrencyText } from "./remark-restore-currency-text";

const remarkPlugins = [
  [remarkMath, { singleDollarTextMath: true }],
  remarkRestoreCurrencyText,
  remarkBreaks,
  remarkGfmWithSharedExtensions,
  remarkCjkFriendly,
  remarkRepairCjkStrongTrailingWhitespace,
] satisfies NonNullable<ReactMarkdownOptions["remarkPlugins"]>;

function renderWith(
  rawPlugin: typeof rehypeRaw | typeof rehypeRawWhenNeeded,
  markdown: string,
  katexPlugin: typeof rehypeKatex | typeof rehypeKatexWhenNeeded,
) {
  let rawTree: Root | undefined;
  let sanitizedTree: Root | undefined;
  const captureRaw = () => (tree: Root) => {
    rawTree = structuredClone(tree);
  };
  const captureSanitized = () => (tree: Root) => {
    sanitizedTree = structuredClone(tree);
  };
  const html = renderToStaticMarkup(
    <ReactMarkdown
      remarkPlugins={remarkPlugins}
      rehypePlugins={[
        rawPlugin,
        captureRaw,
        [rehypeSanitize, markdownSanitizeSchema],
        katexPlugin,
        captureSanitized,
      ]}
    >
      {markdown}
    </ReactMarkdown>,
  );

  if (!rawTree || !sanitizedTree) throw new Error("Markdown pipeline did not capture HAST");
  return { rawTree, sanitizedTree, html };
}

describe("rehypeRaw conservative gate", () => {
  it("matches rehype-raw for real paragraphs, inline nodes, and parsed positions", () => {
    const markdown = "Plain **bold**, *emphasis*, ~~deleted~~, and `inline code`.";
    const baseline = renderWith(rehypeRaw, markdown, rehypeKatex);
    const gated = renderWith(
      rehypeRawWhenNeeded,
      markdown,
      rehypeKatexWhenNeeded,
    );

    expect(gated.rawTree).toEqual(baseline.rawTree);
    expect(gated.sanitizedTree).toEqual(baseline.sanitizedTree);
    expect(gated.html).toBe(baseline.html);
    expect(gated.rawTree.position?.start).toEqual({ line: 1, column: 1, offset: 0 });
    expect(gated.rawTree.position?.end?.offset).toBe(markdown.length);
    expect(gated.html).toContain("<strong>bold</strong>");
    expect(gated.html).toContain("<em>emphasis</em>");
    expect(gated.html).toContain("<del>deleted</del>");
    expect(gated.html).toContain("<code>inline code</code>");
  });

  it.each([
    ["GFM tables", "| a | b |\n| - | - |\n| one | two |"],
    ["task lists", "- [x] done\n- [ ] pending"],
    ["nested blockquotes and lists", "> quote\n>\n> - nested"],
    ["footnote structures", "A note.[^1]\n\n[^1]: Footnote text."],
    ["hard breaks", "first line\nsecond line"],
    ["raw HTML", '<p>before <span class="kept">inline</span></p>'],
  ])("keeps the original transform for %s", (_name, markdown) => {
    const baseline = renderWith(rehypeRaw, markdown, rehypeKatex);
    const gated = renderWith(
      rehypeRawWhenNeeded,
      markdown,
      rehypeKatexWhenNeeded,
    );

    expect(gated.rawTree).toEqual(baseline.rawTree);
    expect(gated.sanitizedTree).toEqual(baseline.sanitizedTree);
    expect(gated.html).toBe(baseline.html);
  });

  it("keeps sanitizer behavior for hostile raw HTML", () => {
    const markdown = '<img src=x onerror="alert(1)">';
    const baseline = renderWith(rehypeRaw, markdown, rehypeKatex);
    const gated = renderWith(
      rehypeRawWhenNeeded,
      markdown,
      rehypeKatexWhenNeeded,
    );

    expect(gated.rawTree).toEqual(baseline.rawTree);
    expect(gated.sanitizedTree).toEqual(baseline.sanitizedTree);
    expect(gated.html).toBe(baseline.html);
    expect(gated.html).toContain('<img src="x"');
    expect(gated.html).not.toContain("onerror");
  });

  it("keeps KaTeX rendering for math nodes outside the fast path", () => {
    const markdown = "$$\nx^2 + y^2\n$$";
    const baseline = renderWith(rehypeRaw, markdown, rehypeKatex);
    const gated = renderWith(
      rehypeRawWhenNeeded,
      markdown,
      rehypeKatexWhenNeeded,
    );

    expect(gated.rawTree).toEqual(baseline.rawTree);
    expect(gated.sanitizedTree).toEqual(baseline.sanitizedTree);
    expect(gated.html).toBe(baseline.html);
    expect(gated.html).toContain("katex");
  });
});
