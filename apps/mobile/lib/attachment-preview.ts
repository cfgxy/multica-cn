export type MobileAttachmentPreviewKind = "markdown" | "pdf" | "unsupported";

export interface MobileAttachmentPreviewSource {
  // `download_url` keeps its Content-Disposition inline. The optional sibling
  // is deliberately download-only and must never be handed to a document view.
  download_url: string;
  attachment_download_url?: string;
}

export function getMobileAttachmentPreviewKind(
  contentType: string,
  filename: string,
): MobileAttachmentPreviewKind {
  const mime = contentType.split(";", 1)[0]?.trim().toLowerCase() ?? "";
  const extension = filename.toLowerCase().split(".").pop() ?? "";
  if (mime === "application/pdf" || extension === "pdf") return "pdf";
  if (
    mime === "text/markdown" ||
    mime === "text/x-markdown" ||
    extension === "md" ||
    extension === "markdown"
  ) {
    return "markdown";
  }
  return "unsupported";
}

export function attachmentPreviewPath(workspaceSlug: string, attachmentId: string) {
  return `/${workspaceSlug}/attachment/${attachmentId}`;
}

/**
 * Resolve the response-scoped, load-intent URL for an in-app document view.
 *
 * The single-attachment endpoint mints a credential-free `download_url` for
 * private proxy storage and signed object stores. `attachment_download_url`
 * is a separate forced-download capability (`dl=1`), so using it for a PDF
 * preview makes the DOM download the file instead of rendering it inline.
 */
export function resolveMobileAttachmentPreviewUrl(
  attachment: MobileAttachmentPreviewSource | null | undefined,
  resolveUrl: (rawUrl: string | null | undefined) => string | null,
): string | null {
  return resolveUrl(attachment?.download_url);
}
