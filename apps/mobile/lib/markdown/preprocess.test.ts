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

/**
 * Issue mention 降级为正文纯文本（RUYI-635 手机端半边）。
 *
 * Web 端 mention 不再渲染内联卡片，正文按普通文本显示、引用聚合到尾部
 * 列表。手机端没有内联卡片可去——enriched 本来就把 mention 画成带下划线
 * 的链接——但「正文纯文本、引用只出现在尾部列表」的规则两端必须一致，
 * 所以管线里加一个 demote pass：issue mention 链接改写为显示 token
 * （编号形态显示编号，UUID mention 显示作者写的标签）。跳转由尾部聚合
 * 列表承担，不再依赖正文链接。
 *
 * 显示 token 的逻辑与 web 正文渲染（rich-content.tsx `data-issue-mention-text`）
 * 严格对齐，落点在 @multica/core/markdown 的共享实现，两端不可能漂移。
 */
describe("preprocessMobileMarkdown — issue mention demote（RUYI-635）", () => {
  it("编号形态 mention 降级为编号纯文本", () => {
    expect(preprocessMobileMarkdown("见 [MUL-7](mention://issue/MUL-7)。")).toBe(
      "见 MUL-7。",
    );
  });

  it("UUID mention 降级为作者写的标签", () => {
    expect(
      preprocessMobileMarkdown(`见 [任务单](mention://issue/${UUID})。`),
    ).toBe("见 任务单。");
  });

  it("空标签 UUID mention 回退到 id 段，引用不消失", () => {
    expect(preprocessMobileMarkdown(`[](mention://issue/${UUID})`)).toBe(UUID);
  });

  it("代码内的 mention 链接保持字面", () => {
    const inline = "用 `[MUL-7](mention://issue/MUL-7)` 标记";
    expect(preprocessMobileMarkdown(inline)).toBe(inline);
  });

  it("降级后再过一遍管线不再变化（幂等）", () => {
    const once = preprocessMobileMarkdown(
      `见 [MUL-7](mention://issue/${UUID}) 与 MUL-8。`,
    );
    expect(preprocessMobileMarkdown(once)).toBe(once);
  });
});
