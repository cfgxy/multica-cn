export type MobileAttachmentPreviewKind = "markdown" | "pdf" | "unsupported";

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

export interface MobileAttachmentPreviewSource {
  download_url: string;
  attachment_download_url?: string;
}

export function resolveMobileAttachmentPreviewUrl(
  attachment: MobileAttachmentPreviewSource | null | undefined,
  resolveUrl: (rawUrl: string | null | undefined) => string | null,
): string | null {
  return resolveUrl(attachment?.download_url);
}

/** Encodes native-fetched bytes for the Expo DOM bridge. */
export function encodeAttachmentBytesForDom(bytes: Uint8Array): string {
  const chunks: string[] = [];
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    chunks.push(String.fromCharCode(...bytes.subarray(offset, offset + chunkSize)));
  }
  return globalThis.btoa(chunks.join(""));
}
