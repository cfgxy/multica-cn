import { describe, expect, it } from "vitest";
import {
  autolinkReferenceTokens,
  dedupeIssueReferences,
  extractIssueReferences,
  identifierMatchesPrefix,
  isIssueIdentifier,
  parseMergeRequestReference,
  parsePullRequestReference,
} from "./issue-references";

const UUID = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed";
const UUID2 = "8f14e45f-ceea-4670-9b1c-0d3e4f5a6b7c";

describe("extractIssueReferences — mention-form links", () => {
  it("collects a real UUID mention with its authored label", () => {
    expect(extractIssueReferences(`See [MUL-7](mention://issue/${UUID}) please.`)).toEqual([
      { ref: UUID, label: "MUL-7", form: "mention" },
    ]);
  });

  it("collects identifier-form mention links (autolinked shape)", () => {
    expect(extractIssueReferences("Ref [MUL-3](mention://issue/MUL-3) here.")).toEqual([
      { ref: "MUL-3", label: "MUL-3", form: "mention" },
    ]);
  });

  it("collects multiple references in order of first appearance", () => {
    const refs = extractIssueReferences(
      `[MUL-7](mention://issue/${UUID}) then MUL-9 then [MUL-8](mention://issue/${UUID2}).`,
    );
    expect(refs.map((r) => r.ref)).toEqual([UUID, "MUL-9", UUID2]);
  });

  it("ignores member / project / comment mentions", () => {
    expect(
      extractIssueReferences(
        `[@Ada](mention://member/${UUID}) [@Roadmap](mention://project/${UUID2}) [💬 c1](mention://comment/abc)`,
      ),
    ).toEqual([]);
  });

  it("returns [] for text without references", () => {
    expect(extractIssueReferences("No references at all.")).toEqual([]);
  });

  it("collects a CJK-adjacent identifier", () => {
    expect(extractIssueReferences("修复 MUL-1 的问题")).toEqual([
      { ref: "MUL-1", label: null, form: "identifier" },
    ]);
  });
});

describe("extractIssueReferences — bare identifiers", () => {
  it("collects a bare identifier as identifier-form", () => {
    expect(extractIssueReferences("Blocked by MUL-123 today.")).toEqual([
      { ref: "MUL-123", label: null, form: "identifier" },
    ]);
  });

  it("keeps a sentence-final identifier (trailing period)", () => {
    expect(extractIssueReferences("See MUL-1.")).toEqual([
      { ref: "MUL-1", label: null, form: "identifier" },
    ]);
  });

  it("rejects lowercase and malformed shapes", () => {
    expect(extractIssueReferences("mul-1 MUL- M-1A ABC- 12AB")).toEqual([]);
  });

  it("skips identifiers inside fenced code blocks", () => {
    expect(
      extractIssueReferences("```\nfix MUL-123\n```\nbut MUL-2 outside."),
    ).toEqual([{ ref: "MUL-2", label: null, form: "identifier" }]);
  });

  it("skips literal mention links inside fenced code blocks", () => {
    expect(
      extractIssueReferences(`\`\`\`\n[MUL-1](mention://issue/${UUID})\n\`\`\``),
    ).toEqual([]);
  });

  it("skips identifiers inside inline code", () => {
    expect(extractIssueReferences("config `MUL-123` stays plain.")).toEqual([]);
  });

  it("skips identifiers inside math spans", () => {
    expect(extractIssueReferences("formula $MUL-1 + 1$ done.")).toEqual([]);
  });

  it("skips identifiers inside an existing markdown link label or href", () => {
    expect(
      extractIssueReferences(`[see MUL-123](https://x.y) and [x](https://h/MUL-9) end`),
    ).toEqual([]);
  });

  it("skips identifiers inside bare URLs", () => {
    expect(
      extractIssueReferences("query https://app.test/ws?issue=MUL-123 for data"),
    ).toEqual([]);
  });

  it("skips identifiers inside emails", () => {
    expect(extractIssueReferences("mail foo@MUL-9.com now")).toEqual([]);
  });

  it("skips dotted filename tails and path segments", () => {
    expect(
      extractIssueReferences("files MUL-123.ts and FOO-1/bar and foo/BAR-1 here"),
    ).toEqual([]);
  });
});

describe("extractIssueReferences — workspace prefix filter (RUYI-705)", () => {
  const RUYI = { workspacePrefix: "RUYI" };

  it("collects zero identifier references for acceptance-criteria numbering (AC-1..AC-5)", () => {
    const text = ["AC-1", "AC-2", "AC-3", "AC-4", "AC-5"]
      .map((n) => `- ${n} 验收条目已达成`)
      .join("\n");
    expect(extractIssueReferences(text, RUYI)).toEqual([]);
  });

  it("keeps matching-prefix bare identifiers and drops the rest in one pass", () => {
    expect(
      extractIssueReferences("Blocked by RUYI-123, regression tracked as AC-1.", RUYI),
    ).toEqual([{ ref: "RUYI-123", label: null, form: "identifier" }]);
  });

  it("matches the prefix case-insensitively", () => {
    expect(
      extractIssueReferences("see RUYI-9", { workspacePrefix: "ruyi" }),
    ).toEqual([{ ref: "RUYI-9", label: null, form: "identifier" }]);
  });

  it("falls through when no prefix is passed (callers without workspace context)", () => {
    expect(extractIssueReferences("AC-1 and RUYI-2")).toEqual([
      { ref: "AC-1", label: null, form: "identifier" },
      { ref: "RUYI-2", label: null, form: "identifier" },
    ]);
  });

  it("treats an empty prefix as unknown (fall-through)", () => {
    expect(extractIssueReferences("AC-1", { workspacePrefix: "" })).toEqual([
      { ref: "AC-1", label: null, form: "identifier" },
    ]);
  });

  it("never filters mention-form references by prefix", () => {
    const text = `见 [RUYI-1](mention://issue/${UUID}) 与 [AC-1](mention://issue/${UUID2})。`;
    expect(extractIssueReferences(text, RUYI)).toEqual([
      { ref: UUID, label: "RUYI-1", form: "mention" },
      { ref: UUID2, label: "AC-1", form: "mention" },
    ]);
  });

  it("keeps skip rules ahead of the prefix filter", () => {
    // An identifier inside code/links/URLs is skipped for shape reasons; the
    // prefix filter only ever removes tokens that would otherwise be collected.
    expect(
      extractIssueReferences("`AC-1` and [x](https://h/AC-2) but RUYI-3 yes", RUYI),
    ).toEqual([{ ref: "RUYI-3", label: null, form: "identifier" }]);
  });
});

describe("identifierMatchesPrefix", () => {
  it("matches an identifier carrying the workspace prefix", () => {
    expect(identifierMatchesPrefix("RUYI-123", "RUYI")).toBe(true);
  });

  it("rejects a different prefix at the dash boundary", () => {
    expect(identifierMatchesPrefix("AC-1", "RUYI")).toBe(false);
    // The dash stops a proper-prefix false positive: RUYI is not a match for
    // RUYIX-1.
    expect(identifierMatchesPrefix("RUYIX-1", "RUYI")).toBe(false);
  });

  it("is case-insensitive on both sides", () => {
    expect(identifierMatchesPrefix("ruyi-1", "RUYI")).toBe(true);
    expect(identifierMatchesPrefix("RUYI-1", "ruyi")).toBe(true);
  });

  it("matches everything when the prefix is unknown", () => {
    expect(identifierMatchesPrefix("AC-1", null)).toBe(true);
    expect(identifierMatchesPrefix("AC-1", undefined)).toBe(true);
    expect(identifierMatchesPrefix("AC-1", "")).toBe(true);
  });
});

describe("dedupeIssueReferences", () => {
  it("keeps the first occurrence per ref+form, preserving order", () => {
    const refs = extractIssueReferences(
      `[MUL-7](mention://issue/${UUID}) again [MUL-7](mention://issue/${UUID}) and MUL-7 then MUL-7.`,
    );
    expect(dedupeIssueReferences(refs)).toEqual([
      { ref: UUID, label: "MUL-7", form: "mention" },
      { ref: "MUL-7", label: null, form: "identifier" },
    ]);
  });
});

describe("isIssueIdentifier", () => {
  it("accepts uppercase prefix + digits", () => {
    expect(isIssueIdentifier("MUL-7")).toBe(true);
    expect(isIssueIdentifier("ABC12-345")).toBe(true);
  });

  it("rejects other shapes", () => {
    expect(isIssueIdentifier(UUID)).toBe(false);
    expect(isIssueIdentifier("mul-7")).toBe(false);
    expect(isIssueIdentifier("MUL-")).toBe(false);
    expect(isIssueIdentifier("M-UL7")).toBe(false);
  });
});

describe("extractIssueReferences — pull request references", () => {
  it("collects a plain owner/repo#N token as pull_request-form", () => {
    expect(extractIssueReferences("Fix landed in cfgxy/multica-cn#338.")).toEqual([
      { ref: "cfgxy/multica-cn#338", label: null, form: "pull_request" },
    ]);
  });

  it("collects PR refs alongside issue refs in first-appearance order", () => {
    const refs = extractIssueReferences(
      "RUYI-635 fixed by cfgxy/multica-cn#338, see also MUL-9.",
    );
    expect(refs.map((r) => `${r.form}:${r.ref}`)).toEqual([
      "identifier:RUYI-635",
      "pull_request:cfgxy/multica-cn#338",
      "identifier:MUL-9",
    ]);
  });

  it("accepts digits and hyphens in owner, dots in repo", () => {
    expect(extractIssueReferences("See 2k/mono.repo#12 done.")).toEqual([
      { ref: "2k/mono.repo#12", label: null, form: "pull_request" },
    ]);
  });

  it("keeps sentence-final punctuation and CJK adjacency", () => {
    expect(extractIssueReferences("已合并 cfgxy/multica-cn#338。")).toEqual([
      { ref: "cfgxy/multica-cn#338", label: null, form: "pull_request" },
    ]);
    expect(extractIssueReferences("(cfgxy/multica-cn#338) 已归档。")).toEqual([
      { ref: "cfgxy/multica-cn#338", label: null, form: "pull_request" },
    ]);
  });

  it("skips PR refs inside code spans", () => {
    expect(extractIssueReferences("跑 `git cfgxy/multica-cn#338` 看看。")).toEqual([]);
  });

  it("skips PR refs that are part of a URL (fragment or path)", () => {
    expect(
      extractIssueReferences("https://github.com/cfgxy/multica-cn/pull/338#issuecomment-1"),
    ).toEqual([]);
    expect(
      extractIssueReferences("打开 https://github.com/cfgxy/multica-cn#338 查看。"),
    ).toEqual([]);
  });

  it("skips path-segment shapes (owner/repo#N inside a larger path)", () => {
    expect(extractIssueReferences("mirror at backup/cfgxy/multica-cn#338 跳过。")).toEqual([]);
    expect(extractIssueReferences("cfgxy/multica-cn#338/commits 跳过。")).toEqual([]);
  });

  it("skips a PR ref whose number runs into a dotted name", () => {
    expect(extractIssueReferences("artifact cfgxy/multica-cn#338.tar.gz 跳过。")).toEqual([]);
  });

  it("skips a PR ref glued to a dotted name on the left", () => {
    expect(extractIssueReferences("见 file.cfgxy/multica-cn#338 跳过。")).toEqual([]);
  });

  it("dedupes repeated PR tokens to the first occurrence", () => {
    const refs = dedupeIssueReferences(
      extractIssueReferences("cfgxy/multica-cn#338 又提一次 cfgxy/multica-cn#338。"),
    );
    expect(refs).toHaveLength(1);
    expect(refs[0]).toEqual({ ref: "cfgxy/multica-cn#338", label: null, form: "pull_request" });
  });
});

describe("parsePullRequestReference", () => {
  it("splits owner, repo and number", () => {
    expect(parsePullRequestReference("cfgxy/multica-cn#338")).toEqual({
      owner: "cfgxy",
      repo: "multica-cn",
      number: 338,
    });
  });

  it("returns null for tokens the extractor would never emit", () => {
    expect(parsePullRequestReference("MUL-7")).toBeNull();
    expect(parsePullRequestReference("owner/repo")).toBeNull();
    expect(parsePullRequestReference("owner/repo#abc")).toBeNull();
    expect(parsePullRequestReference("")).toBeNull();
  });
});

describe("extractIssueReferences — MR text references (RUYI-728)", () => {
  it("collects a plain MR !N token as merge_request-form with a normalized ref", () => {
    expect(extractIssueReferences("已合并 MR !468。")).toEqual([
      { ref: "MR !468", label: null, form: "merge_request" },
    ]);
  });

  it("normalizes the no-space MR!N spelling to MR !N", () => {
    expect(extractIssueReferences("见 MR!69 说明。")).toEqual([
      { ref: "MR !69", label: null, form: "merge_request" },
    ]);
  });

  it("dedupes the spaced and no-space spellings to one token", () => {
    const refs = dedupeIssueReferences(
      extractIssueReferences("MR !468 与 MR!468 是同一个。"),
    );
    expect(refs).toHaveLength(1);
    expect(refs[0]).toEqual({ ref: "MR !468", label: null, form: "merge_request" });
  });

  it("collects MR refs alongside issue refs in first-appearance order", () => {
    const refs = extractIssueReferences(
      "IDAT-55 已由 MR !468 修复，IDAT-54 跟进 MR !69。",
    );
    expect(refs.map((r) => `${r.form}:${r.ref}`)).toEqual([
      "identifier:IDAT-55",
      "merge_request:MR !468",
      "identifier:IDAT-54",
      "merge_request:MR !69",
    ]);
  });

  it("keeps sentence-final punctuation and CJK adjacency", () => {
    expect(extractIssueReferences("(MR !468) 已归档。")).toEqual([
      { ref: "MR !468", label: null, form: "merge_request" },
    ]);
  });

  it("rejects lowercase spelling", () => {
    expect(extractIssueReferences("mr !468 不是引用。")).toEqual([]);
  });

  it("skips MR refs inside code spans and fenced blocks", () => {
    expect(extractIssueReferences("跑 `git MR !468` 看看。")).toEqual([]);
    expect(extractIssueReferences("```\nMR !468\n```")).toEqual([]);
  });

  it("skips MR refs inside an existing markdown link", () => {
    expect(
      extractIssueReferences("[MR !468](https://gitlab.test/a/b/-/merge_requests/468)"),
    ).toEqual([]);
  });

  it("skips MR refs that are part of a URL", () => {
    expect(extractIssueReferences("打开 https://example.com/MR !468 查看。")).toEqual([]);
  });

  it("skips an MR ref whose number runs into a dotted name or path", () => {
    expect(extractIssueReferences("artifact MR !468.tar.gz 跳过。")).toEqual([]);
    expect(extractIssueReferences("MR !468/commits 跳过。")).toEqual([]);
  });
});

describe("parseMergeRequestReference", () => {
  it("returns the number", () => {
    expect(parseMergeRequestReference("MR !468")).toEqual({ number: 468 });
    expect(parseMergeRequestReference("MR !69")).toEqual({ number: 69 });
  });

  it("returns null for tokens the extractor would never emit", () => {
    expect(parseMergeRequestReference("MR !")).toBeNull();
    expect(parseMergeRequestReference("MR !abc")).toBeNull();
    expect(parseMergeRequestReference("MUL-7")).toBeNull();
    expect(parseMergeRequestReference("")).toBeNull();
  });
});

describe("autolinkReferenceTokens (RUYI-728 readonly body rewrite)", () => {
  it("rewrites MR !N into a mention://merge-request link", () => {
    expect(autolinkReferenceTokens("已合并 MR !468。")).toBe(
      "已合并 [MR !468](mention://merge-request/468)。",
    );
  });

  it("keeps the authored no-space spelling as the visible label", () => {
    expect(autolinkReferenceTokens("见 MR!69 说明。")).toBe(
      "见 [MR!69](mention://merge-request/69) 说明。",
    );
  });

  it("rewrites owner/repo#N into the canonical GitHub pull URL", () => {
    expect(autolinkReferenceTokens("Fix landed in cfgxy/multica-cn#338.")).toBe(
      "Fix landed in [cfgxy/multica-cn#338](https://github.com/cfgxy/multica-cn/pull/338).",
    );
  });

  it("rewrites both forms in one pass, leaving other text alone", () => {
    expect(
      autolinkReferenceTokens("IDAT-55 由 MR !468 修复，对照 cfgxy/multica-cn#338。"),
    ).toBe(
      "IDAT-55 由 [MR !468](mention://merge-request/468) 修复，对照 [cfgxy/multica-cn#338](https://github.com/cfgxy/multica-cn/pull/338)。",
    );
  });

  it("skips code spans, existing links and bare URLs", () => {
    const fenced = "```\nMR !468\n```";
    expect(autolinkReferenceTokens(fenced)).toBe(fenced);
    expect(autolinkReferenceTokens("跑 `git MR !468` 看看。")).toBe(
      "跑 `git MR !468` 看看。",
    );
    expect(
      autolinkReferenceTokens("[MR !468](https://gitlab.test/a/b/-/merge_requests/468)"),
    ).toBe("[MR !468](https://gitlab.test/a/b/-/merge_requests/468)");
    expect(
      autolinkReferenceTokens("打开 https://github.com/cfgxy/multica-cn#338 查看。"),
    ).toBe("打开 https://github.com/cfgxy/multica-cn#338 查看。");
  });

  it("skips dotted tails and path continuations", () => {
    expect(autolinkReferenceTokens("artifact MR !468.tar.gz 跳过。")).toBe(
      "artifact MR !468.tar.gz 跳过。",
    );
    expect(autolinkReferenceTokens("MR !468/commits 跳过。")).toBe(
      "MR !468/commits 跳过。",
    );
  });

  it("does not touch bare issue identifiers (the identifier pass owns those)", () => {
    expect(autolinkReferenceTokens("IDAT-55 修复了。")).toBe("IDAT-55 修复了。");
  });
});
