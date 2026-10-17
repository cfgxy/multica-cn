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
 * 正文引用链接化（RUYI-728 手机端半边）。
 *
 * Web 端新契约：issue mention 链接保持可点（App 内路由），正文里的
 * `MR !N` 与 `owner/repo#N` 裸文本由 autolink pass 改写成链接——MR 走
 * `mention://merge-request/<N>`（点击时按编号解析为 PR 后交系统打开），
 * PR 走 canonical GitHub URL（GitHub 自动重定向 /pull/N ↔ /issues/N）。
 * 跳转决策在 link-route.ts，这里锚定改写本身；实现落点在
 * @multica/core/markdown 的共享 autolinkReferenceTokens，与 web/desktop
 * 共用，两端不可能漂移。
 */
describe("preprocessMobileMarkdown — 引用链接化（RUYI-728）", () => {
  it("仅把当前 workspace prefix 的裸 Issue 编号链接到应用内路由", () => {
    expect(preprocessMobileMarkdown("RUYI-355 和 AC-1", { workspacePrefix: "RUYI" })).toBe(
      "[RUYI-355](mention://issue/RUYI-355) 和 AC-1",
    );
  });

  it("裸 Issue 编号保留代码、已有链接与路径内容", () => {
    const input = "`RUYI-355` [RUYI-355](https://example.test) /x/RUYI-355";
    expect(preprocessMobileMarkdown(input, { workspacePrefix: "RUYI" })).toBe(input);
  });

  it.each([null, undefined])(
    "前缀未解析时保留裸 Issue 标识符（workspacePrefix=%s）",
    (workspacePrefix) => {
      const input = "RUYI-1 and AC-2";
      expect(preprocessMobileMarkdown(input, { workspacePrefix })).toBe(input);
    },
  );

  it("正文 PR #N 改写为 pull-request 动作链接", () => {
    expect(preprocessMobileMarkdown("见 PR #412。")).toBe(
      "见 [PR #412](mention://pull-request/412)。",
    );
  });

  it("issue mention 链接保持为链接（不再降级为纯文本）", () => {
    const link = `[MUL-7](mention://issue/MUL-7)`;
    expect(preprocessMobileMarkdown(`见 ${link}。`)).toBe(`见 ${link}。`);
  });

  it("UUID mention 链接同样保持为链接", () => {
    const link = `[任务单](mention://issue/${UUID})`;
    expect(preprocessMobileMarkdown(`见 ${link}。`)).toBe(`见 ${link}。`);
  });

  it("裸 MR 编号改写为 mention://merge-request 链接", () => {
    expect(preprocessMobileMarkdown("见 MR !468。")).toBe(
      "见 [MR !468](mention://merge-request/468)。",
    );
  });

  it("owner/repo#N 改写为 canonical GitHub PR 链接", () => {
    expect(preprocessMobileMarkdown("见 cfgxy/multica-cn#338。")).toBe(
      "见 [cfgxy/multica-cn#338](https://github.com/cfgxy/multica-cn/pull/338)。",
    );
  });

  it("代码内的引用 token 保持字面", () => {
    const inline = "用 `MR !468` 与 `[MUL-7](mention://issue/MUL-7)` 标记";
    expect(preprocessMobileMarkdown(inline)).toBe(inline);
  });

  it("改写后再过一遍管线不再变化（幂等）", () => {
    const once = preprocessMobileMarkdown(
      "见 MR !468 与 cfgxy/multica-cn#338。",
    );
    expect(preprocessMobileMarkdown(once)).toBe(once);
  });
});
