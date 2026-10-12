import { describe, expect, it } from "vitest";
import {
  preprocessIssueIdentifiers,
  isIssueIdentifier,
} from "@multica/ui/markdown";

/**
 * Pure detector for the Linear-style issue-identifier autolink. Lives in
 * @multica/ui/markdown (no test runner there), exercised here where views'
 * vitest can reach it.
 */
describe("preprocessIssueIdentifiers", () => {
  it("rewrites a bare identifier into a canonical mention link", () => {
    expect(preprocessIssueIdentifiers("Related to MUL-1745")).toBe(
      "Related to [MUL-1745](mention://issue/MUL-1745)",
    );
  });

  it("rewrites multiple identifiers in one string", () => {
    expect(preprocessIssueIdentifiers("Created TES-1 and MUL-2")).toBe(
      "Created [TES-1](mention://issue/TES-1) and [MUL-2](mention://issue/MUL-2)",
    );
  });

  it("links an identifier at a sentence end (trailing dot + space)", () => {
    expect(preprocessIssueIdentifiers("See MUL-1. Done.")).toBe(
      "See [MUL-1](mention://issue/MUL-1). Done.",
    );
  });

  it("links identifiers wrapped in prose punctuation", () => {
    expect(preprocessIssueIdentifiers("(MUL-1) and [MUL-2]")).toContain(
      "([MUL-1](mention://issue/MUL-1))",
    );
  });

  // --- skip: code -------------------------------------------------------
  it("skips identifiers inside inline code", () => {
    expect(preprocessIssueIdentifiers("use `MUL-1` here")).toBe(
      "use `MUL-1` here",
    );
  });

  it("skips identifiers inside fenced code blocks", () => {
    const input = "```\nMUL-1 in code\n```";
    expect(preprocessIssueIdentifiers(input)).toBe(input);
  });

  // --- skip: existing links / mentions ----------------------------------
  it("does not double-process an existing mention link", () => {
    const input = "[MUL-1](mention://issue/00000000-0000-0000-0000-000000000001)";
    expect(preprocessIssueIdentifiers(input)).toBe(input);
  });

  it("skips an identifier used as a markdown link label", () => {
    const input = "[MUL-1](https://example.com/x)";
    expect(preprocessIssueIdentifiers(input)).toBe(input);
  });

  // --- skip: urls / filenames / paths -----------------------------------
  it("skips an identifier inside a URL", () => {
    const input = "https://example.com/board/MUL-1";
    expect(preprocessIssueIdentifiers(input)).toBe(input);
  });

  it("skips a filename token like ABC-123.ts", () => {
    const input = "open ABC-123.ts now";
    expect(preprocessIssueIdentifiers(input)).toBe(input);
  });

  it("skips a path segment like FOO-1/bar", () => {
    const input = "path FOO-1/bar/baz";
    expect(preprocessIssueIdentifiers(input)).toBe(input);
  });

  // --- non-matches ------------------------------------------------------
  it("ignores lowercase tokens", () => {
    const input = "some-word-1 and mul-1";
    expect(preprocessIssueIdentifiers(input)).toBe(input);
  });

  it("ignores a token embedded in a larger word", () => {
    const input = "XMUL-1A stays";
    expect(preprocessIssueIdentifiers(input)).toBe(input);
  });

  it("returns input unchanged when no candidates exist", () => {
    const input = "plain text with no identifiers";
    expect(preprocessIssueIdentifiers(input)).toBe(input);
  });

  // --- workspace prefix filter (RUYI-705) --------------------------------
  // Same gate as core's extractIssueReferences — the two copies must agree on
  // which tokens are issue references, or the tail list and the body drift.
  it("leaves non-matching-prefix tokens as plain text", () => {
    const input = "AC-1 and AC-2 stay plain";
    expect(
      preprocessIssueIdentifiers(input, { workspacePrefix: "MUL" }),
    ).toBe(input);
  });

  it("rewrites matching-prefix tokens and leaves the rest in one pass", () => {
    expect(
      preprocessIssueIdentifiers("MUL-1 and AC-1", { workspacePrefix: "MUL" }),
    ).toBe("[MUL-1](mention://issue/MUL-1) and AC-1");
  });

  it("matches the prefix case-insensitively", () => {
    expect(
      preprocessIssueIdentifiers("MUL-1 done", { workspacePrefix: "mul" }),
    ).toBe("[MUL-1](mention://issue/MUL-1) done");
  });

  it("falls through when no prefix is given", () => {
    expect(preprocessIssueIdentifiers("AC-1")).toBe(
      "[AC-1](mention://issue/AC-1)",
    );
  });

  it("treats an empty prefix as unknown (fall-through)", () => {
    expect(preprocessIssueIdentifiers("AC-1", { workspacePrefix: "" })).toBe(
      "[AC-1](mention://issue/AC-1)",
    );
  });
});

describe("isIssueIdentifier", () => {
  it("accepts a bare identifier", () => {
    expect(isIssueIdentifier("MUL-1745")).toBe(true);
    expect(isIssueIdentifier("TES-1")).toBe(true);
  });

  it("rejects a UUID (so real mentions are not treated as identifiers)", () => {
    expect(isIssueIdentifier("00000000-0000-0000-0000-000000000001")).toBe(
      false,
    );
  });

  it("rejects lowercase and malformed tokens", () => {
    expect(isIssueIdentifier("mul-1")).toBe(false);
    expect(isIssueIdentifier("MUL-")).toBe(false);
    expect(isIssueIdentifier("MUL1")).toBe(false);
  });
});
