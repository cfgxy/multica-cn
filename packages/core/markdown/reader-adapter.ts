export const MARKDOWN_READER_V2_FLAG = "markdown_reader_v2";

export type MarkdownRenderer = "legacy" | "modern";

export function selectMarkdownRenderer(
  featureFlags: Readonly<Record<string, boolean>> | null | undefined,
): MarkdownRenderer {
  return featureFlags?.[MARKDOWN_READER_V2_FLAG] === true
    ? "modern"
    : "legacy";
}
