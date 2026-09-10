import { describe, expect, it } from "vitest";
import { preprocessMobileMarkdown } from "./preprocess";

const UUID = "019f49e2-5b07-7970-beef-c0d537fb8c1d";
const ABS_URL = `https://multica-app.copilothub.ai/api/attachments/${UUID}/download`;
const REL_URL = `/api/attachments/${UUID}/download`;

describe("preprocessMobileMarkdown — !file file cards", () => {
  it("keeps channel images visible while hiding their provenance marker", () => {
    const image = `![](${REL_URL})`;
    const marker = `<!-- multica:channel-media:${UUID} -->`;

    expect(preprocessMobileMarkdown(`${image}\n\n${marker}`)).toBe(`${image}\n\n`);
  });

  it("matches the CLI's escaped-bracket label and keeps it markdown-safe", () => {
    // CLI emits `a]b.pdf` escaped as `a\]b.pdf` (cmd_attachment.go
    // escapeMarkdownLabel). The old regex stopped at the first `]` and left the
    // line literal; now it is captured whole and re-emitted as a tappable link
    // whose label stays escaped so the `]` doesn't truncate the link text.
    const out = preprocessMobileMarkdown(`!file[a\\]b.pdf](${ABS_URL})`);
    expect(out).toBe(`[📎 a\\]b.pdf](${ABS_URL})`);
  });

  it("unescapes non-breaking metacharacters (parens) in the displayed name", () => {
    // Parens are legal inside a markdown link label, so they are unescaped for
    // display and not re-escaped.
    const out = preprocessMobileMarkdown(`!file[report\\(1\\).pdf](${ABS_URL})`);
    expect(out).toBe(`[📎 report(1).pdf](${ABS_URL})`);
  });

  it("unescapes an escaped backslash in the label", () => {
    const out = preprocessMobileMarkdown(`!file[a\\\\b.pdf](${ABS_URL})`);
    expect(out).toBe(`[📎 a\\\\b.pdf](${ABS_URL})`);
  });

  it("renders a plain (unescaped) label", () => {
    const out = preprocessMobileMarkdown(`!file[notes.txt](${ABS_URL})`);
    expect(out).toBe(`[📎 notes.txt](${ABS_URL})`);
  });

  it("accepts the site-relative /api/attachments URL form (web parity)", () => {
    const out = preprocessMobileMarkdown(`!file[a.pdf](${REL_URL})`);
    expect(out).toBe(`[📎 a.pdf](${REL_URL})`);
  });

  it("leaves a disallowed-scheme URL as plain text (no out-of-band navigation)", () => {
    const line = `!file[x.pdf](javascript:alert(1))`;
    expect(preprocessMobileMarkdown(line)).toBe(line);
  });

  it("does not touch inline images (![...] is not a file card)", () => {
    const line = `![chart.png](${ABS_URL})`;
    expect(preprocessMobileMarkdown(line)).toBe(line);
  });

  it("only transforms the standalone file-card line, leaving surrounding text", () => {
    const input = `here is the file\n\n!file[a\\]b.pdf](${ABS_URL})\n\nlet me know`;
    const output = `here is the file\n\n[📎 a\\]b.pdf](${ABS_URL})\n\nlet me know`;
    expect(preprocessMobileMarkdown(input)).toBe(output);
  });
});

/**
 * 评论锚点在手机端的 chip 形态（RUYI-108）。
 *
 * enriched 不接受注入 React，Web 那个 `CommentMentionCard` 组件在这里无法复用，
 * chip 感由两半拼出来：本文件加 💬 前缀（图标位），`markdown-style.ts` 的
 * `linkVariants` 给底色并去掉下划线。这里测前一半——它必须只作用于评论锚点，
 * 且反复处理同一段内容不能不断堆前缀（WS 更新会让同一条正文多次过管线）。
 */
describe("preprocessMobileMarkdown — 评论锚点 chip", () => {
  const ANCHOR = `mention://comment/${UUID}`;

  it("给评论锚点加 💬 前缀，链接目标不变", () => {
    expect(preprocessMobileMarkdown(`见 [前次交付卡](${ANCHOR})`)).toBe(
      `见 [💬 前次交付卡](${ANCHOR})`,
    );
  });

  it("重复处理不叠加前缀", () => {
    const once = preprocessMobileMarkdown(`[交付卡](${ANCHOR})`);
    expect(preprocessMobileMarkdown(once)).toBe(once);
  });

  it("空标签也能得到可点击的目标", () => {
    expect(preprocessMobileMarkdown(`[](${ANCHOR})`)).toBe(`[💬 ](${ANCHOR})`);
  });

  it("不碰其他 mention 形态和普通链接", () => {
    const others = [
      `[MUL-1](mention://issue/${UUID})`,
      `[@Bob](mention://agent/${UUID})`,
      `[项目](mention://project/${UUID})`,
      `[站点](https://example.com/mention://comment/x)`,
    ].join("\n\n");
    expect(preprocessMobileMarkdown(others)).toBe(others);
  });

  it("同一段里多个锚点各自加前缀", () => {
    const out = preprocessMobileMarkdown(`[a](${ANCHOR}) 与 [b](${ANCHOR})`);
    expect(out).toBe(`[💬 a](${ANCHOR}) 与 [💬 b](${ANCHOR})`);
  });
});
