// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  MARKDOWN_READER_V2_FLAG,
  selectMarkdownRenderer,
} from "./reader-adapter";

describe("selectMarkdownRenderer", () => {
  it("keeps the legacy renderer for absent, malformed, or disabled flags", () => {
    expect(selectMarkdownRenderer(undefined)).toBe("legacy");
    expect(selectMarkdownRenderer(null)).toBe("legacy");
    expect(selectMarkdownRenderer({})).toBe("legacy");
    expect(selectMarkdownRenderer({ [MARKDOWN_READER_V2_FLAG]: false })).toBe(
      "legacy",
    );
    expect(selectMarkdownRenderer({ [MARKDOWN_READER_V2_FLAG]: 1 as never })).toBe(
      "legacy",
    );
  });

  it("selects the modern renderer only for an explicit true decision", () => {
    expect(selectMarkdownRenderer({ [MARKDOWN_READER_V2_FLAG]: true })).toBe(
      "modern",
    );
  });
});
