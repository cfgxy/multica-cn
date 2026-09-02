/**
 * Markdown-side helpers for the new-issue description attachment flow —
 * the mobile mirror of web's create-issue upload binding (RUYI-42).
 *
 * Web model (packages/views/modals/create-issue.tsx + MUL-5181): a picked
 * file uploads immediately, its durable link is inserted into the body,
 * and at submit time `attachment_ids` carries ONLY the uploads the final
 * body still references — deleting the reference line really unbinds the
 * file. Mobile's description input is the same plain-markdown surface
 * (raw link text in, rendered output on the detail screen), so the same
 * three pieces apply:
 *
 *   - `pickMarkdownLink`  — which URL to write into the body. Mirrors
 *     `pickMarkdownLink` in packages/core/hooks/use-file-upload.ts:87
 *     (MUL-3192): durable `markdown_url` first, legacy site-relative
 *     download path second, raw storage url last.
 *   - `attachmentMarkdown` — the fragment shape. Mirrors
 *     `attachmentMarkdown` in packages/views/editor/use-coordinated-uploads.ts:104
 *     (image node `![name](link)` vs file link `[name](link)`).
 *   - `referencedAttachmentIds` — the submit-time binding filter. Mirrors
 *     create-issue.tsx's `activeAttachmentIds` derivation, using the
 *     shared pure `contentReferencesAttachment` from `@multica/core/types`
 *     (same import `lib/attachment-dedup.ts` already relies on) so URL-form
 *     parity with web cannot drift.
 *
 * Mirrored rather than imported because `use-file-upload.ts` pulls the
 * React hook module and `use-coordinated-uploads.ts` lives in
 * `packages/views` — neither is on the mobile import whitelist
 * (apps/mobile/CLAUDE.md "What mobile may import from packages/").
 */
import type { Attachment } from "@multica/core/types";
import {
  attachmentDownloadPath,
  contentReferencesAttachment,
} from "@multica/core/types";

export function pickMarkdownLink(att: Attachment): string {
  if (att.markdown_url) return att.markdown_url;
  if (att.id) return attachmentDownloadPath(att.id);
  return att.url;
}

export function attachmentMarkdown(att: Attachment): string {
  const link = pickMarkdownLink(att);
  return (att.content_type ?? "").startsWith("image/")
    ? `![${att.filename}](${link})`
    : `[${att.filename}](${link})`;
}

/** Append a markdown fragment on its own line at the end of `text`,
 *  tolerating empty bodies and pre-existing trailing newlines. */
export function appendBodyMarkdown(text: string, markdown: string): string {
  if (text.trim().length === 0) return markdown;
  return text.endsWith("\n") ? `${text}${markdown}` : `${text}\n${markdown}`;
}

/** Web parity: bind ONLY uploads the final body still references —
 *  deleting the reference line really unbinds the file. */
export function referencedAttachmentIds(
  attachments: Attachment[],
  body: string,
): string[] {
  return attachments
    .filter((a) => contentReferencesAttachment(body, a))
    .map((a) => a.id);
}
