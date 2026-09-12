export type AttachmentZoneStatus = "uploading" | "completed" | "failed";

export interface AttachmentZoneItem {
  localId: string;
  localUri: string;
  filename: string;
  mimeType: string;
  status: AttachmentZoneStatus;
  id?: string;
  url?: string;
  downloadUrl?: string;
  error?: string;
}

export function groupAttachmentZoneItems(
  items: readonly AttachmentZoneItem[],
): {
  images: AttachmentZoneItem[];
  files: AttachmentZoneItem[];
} {
  const images: AttachmentZoneItem[] = [];
  const files: AttachmentZoneItem[] = [];
  for (const item of items) {
    (item.mimeType.startsWith("image/") ? images : files).push(item);
  }
  return { images, files };
}

export function completedAttachmentIds(
  items: readonly AttachmentZoneItem[],
): string[] {
  return items
    .filter((item) => item.status === "completed")
    .map((item) => item.id)
    .filter((id): id is string => !!id);
}

export function hasUploadingAttachments(
  items: readonly AttachmentZoneItem[],
): boolean {
  return items.some((item) => item.status === "uploading");
}

export function buildManualCreateContentFields(
  description: string,
  attachments: readonly AttachmentZoneItem[],
): { description?: string; attachment_ids?: string[] } {
  const normalizedDescription = description.trim();
  const attachmentIds = completedAttachmentIds(attachments);
  return {
    ...(normalizedDescription.length > 0
      ? { description: normalizedDescription }
      : {}),
    ...(attachmentIds.length > 0 ? { attachment_ids: attachmentIds } : {}),
  };
}

export function canSubmitMessageDraft({
  text,
  mentions,
  attachments,
  disabled = false,
  isSending = false,
  submitting = false,
  requireVisibleText = false,
}: {
  text: string;
  mentions: readonly unknown[];
  attachments: readonly AttachmentZoneItem[];
  disabled?: boolean;
  isSending?: boolean;
  submitting?: boolean;
  requireVisibleText?: boolean;
}): boolean {
  if (
    disabled ||
    isSending ||
    submitting ||
    hasUploadingAttachments(attachments)
  ) {
    return false;
  }
  if (text.trim().length > 0) return true;
  return !requireVisibleText && mentions.length > 0;
}
