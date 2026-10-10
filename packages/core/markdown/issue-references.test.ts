import { describe, expect, it } from "vitest";
import {
  dedupeIssueReferences,
  demoteIssueMentionLinks,
  extractIssueReferences,
  isIssueIdentifier,
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

/**
 * demoteIssueMentionLinks — the mobile body-text half of RUYI-635. The display
 * token must match web's plain-text mention rendering exactly: identifier form
 * shows the identifier, a UUID mention shows its authored label. Code spans and
 * fenced blocks keep their literal link text (same skip contract as the
 * extractor), and every other mention scheme is untouched.
 */
describe("demoteIssueMentionLinks", () => {
  it("demotes an identifier-form mention to the bare identifier", () => {
    expect(demoteIssueMentionLinks("See [MUL-7](mention://issue/MUL-7).")).toBe(
      "See MUL-7.",
    );
  });

  it("demotes a UUID mention to its authored label", () => {
    expect(
      demoteIssueMentionLinks(`See [MUL-7](mention://issue/${UUID}).`),
    ).toBe("See MUL-7.");
  });

  it("falls back to the id segment when a UUID mention has an empty label", () => {
    expect(demoteIssueMentionLinks(`[](${`mention://issue/${UUID}`})`)).toBe(
      `mention://issue/${UUID}`.split("/").pop()!,
    );
  });

  it("leaves code spans and fenced blocks literal", () => {
    const inline = "use `[MUL-7](mention://issue/MUL-7)` here";
    expect(demoteIssueMentionLinks(inline)).toBe(inline);
    const fenced = "```\n[MUL-7](mention://issue/MUL-7)\n```";
    expect(demoteIssueMentionLinks(fenced)).toBe(fenced);
  });

  it("leaves non-issue mention schemes alone", () => {
    const text = [
      `[@Bob](mention://agent/${UUID})`,
      `[项目](mention://project/${UUID})`,
      `[锚点](mention://comment/${UUID})`,
    ].join("\n");
    expect(demoteIssueMentionLinks(text)).toBe(text);
  });

  it("is idempotent — demoted text passes through unchanged", () => {
    const once = demoteIssueMentionLinks(
      `See [MUL-7](mention://issue/${UUID}) and MUL-8.`,
    );
    expect(demoteIssueMentionLinks(once)).toBe(once);
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
