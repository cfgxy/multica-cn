// @vitest-environment node
import { describe, expect, it } from "vitest";
import { diffPromptText } from "./prompt-diff";

// This diff is what a user reads before agreeing to overwrite a prompt they
// wrote. A wrong diff here is worse than no diff: it would show an approval
// for a change that is not the change being made.

describe("diffPromptText", () => {
  it("reports no changes for identical text", () => {
    const result = diffPromptText("a\nb\nc", "a\nb\nc");
    expect(result.added).toBe(0);
    expect(result.removed).toBe(0);
    expect(result.lines.every((line) => line.kind === "context")).toBe(true);
  });

  it("marks an empty target as pure addition", () => {
    const result = diffPromptText("", "hello\nworld");
    expect(result.removed).toBe(0);
    expect(result.added).toBe(2);
    expect(result.lines.map((line) => line.kind)).toEqual(["added", "added"]);
  });

  it("keeps the unchanged lines as context around an edit", () => {
    const result = diffPromptText("a\nb\nc", "a\nB\nc");
    expect(result.added).toBe(1);
    expect(result.removed).toBe(1);
    expect(result.lines.filter((line) => line.kind === "context")).toHaveLength(2);
    expect(result.lines.find((line) => line.kind === "removed")?.text).toBe("b");
    expect(result.lines.find((line) => line.kind === "added")?.text).toBe("B");
  });

  it("numbers each side independently", () => {
    const result = diffPromptText("a\nb", "a\nx\nb");
    const addedLine = result.lines.find((line) => line.kind === "added");
    expect(addedLine?.currentLine).toBeNull();
    expect(addedLine?.incomingLine).toBe(2);
    const lastContext = result.lines.filter((line) => line.kind === "context").at(-1);
    expect(lastContext?.currentLine).toBe(2);
    expect(lastContext?.incomingLine).toBe(3);
  });

  it("does not report a CRLF round trip as a rewrite", () => {
    const result = diffPromptText("a\r\nb\r\nc", "a\nb\nc");
    expect(result.added).toBe(0);
    expect(result.removed).toBe(0);
  });

  it("degrades to a whole-text replacement past the size cap", () => {
    const huge = new Array(4001).fill("line").join("\n");
    const result = diffPromptText(huge, "short");
    expect(result.truncated).toBe(true);
    expect(result.removed).toBe(4001);
    expect(result.added).toBe(1);
  });
});
