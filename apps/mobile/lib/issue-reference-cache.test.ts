import { describe, expect, it } from "vitest";
import { cachedIssueReferences } from "./issue-reference-cache";

describe("cachedIssueReferences", () => {
  it("reuses parsed references for matching content and workspace", () => {
    const content = "See E2ER-90217";
    const first = cachedIssueReferences(content, "E2ER");

    expect(cachedIssueReferences(content, "E2ER")).toBe(first);
    expect(cachedIssueReferences(content, "OTHER")).not.toBe(first);
  });

  it("does not cache oversized comment bodies", () => {
    const content = `See E2ER-90218 ${"x".repeat(262_144)}`;
    const first = cachedIssueReferences(content, "E2ER");

    expect(cachedIssueReferences(content, "E2ER")).not.toBe(first);
  });
});
