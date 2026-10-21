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
