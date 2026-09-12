// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  buildManualCreateContentFields,
  canSubmitMessageDraft,
  completedAttachmentIds,
  groupAttachmentZoneItems,
  hasUploadingAttachments,
  type AttachmentZoneItem,
} from "@/lib/attachment-zone";
import type { MentionMarker } from "@/lib/mention-serialize";

function attachment(
  localId: string,
  mimeType: string,
  status: AttachmentZoneItem["status"] = "completed",
  id: string | undefined = `server-${localId}`,
): AttachmentZoneItem {
  return {
    localId,
    localUri: `file:///${localId}`,
    filename: `${localId}.bin`,
    mimeType,
    status,
    id,
  };
}

describe("groupAttachmentZoneItems", () => {
  it("puts images above files while preserving join order inside each group", () => {
    const items = [
      attachment("file-1", "application/pdf"),
      attachment("image-1", "image/png"),
      attachment("file-2", "text/plain"),
      attachment("image-2", "image/jpeg"),
    ];

    const grouped = groupAttachmentZoneItems(items);

    expect(grouped.images.map((item) => item.localId)).toEqual([
      "image-1",
      "image-2",
    ]);
    expect(grouped.files.map((item) => item.localId)).toEqual([
      "file-1",
      "file-2",
    ]);
  });
});

describe("completedAttachmentIds", () => {
  it("keeps only completed server ids in original attachment order", () => {
    const items = [
      attachment("image-1", "image/png", "completed", "att-1"),
      attachment("file-uploading", "application/pdf", "uploading", undefined),
      attachment("file-1", "application/pdf", "completed", "att-2"),
      attachment("image-failed", "image/jpeg", "failed", undefined),
      attachment("missing-id", "text/plain", "completed", ""),
    ];

    expect(completedAttachmentIds(items)).toEqual(["att-1", "att-2"]);
  });
});

describe("buildManualCreateContentFields", () => {
  it("keeps the description independent from completed attachment ids", () => {
    const fields = buildManualCreateContentFields("  Plain description  ", [
      attachment("image-1", "image/png", "completed", "att-1"),
      attachment("file-uploading", "application/pdf", "uploading", undefined),
      attachment("file-1", "application/pdf", "completed", "att-2"),
    ]);

    expect(fields).toEqual({
      description: "Plain description",
      attachment_ids: ["att-1", "att-2"],
    });
    expect(fields.description).not.toContain("![");
    expect(fields.description).not.toContain("/api/attachments/");
  });
});

describe("canSubmitMessageDraft", () => {
  const mention: MentionMarker = {
    type: "member",
    id: "user-1",
    name: "Bohan",
  };

  it("does not let attachments or mentions bypass a visible comment body", () => {
    expect(
      canSubmitMessageDraft({
        text: "",
        mentions: [],
        attachments: [attachment("image-1", "image/png")],
        requireVisibleText: true,
      }),
    ).toBe(false);
    expect(
      canSubmitMessageDraft({
        text: "   ",
        mentions: [mention],
        attachments: [],
        requireVisibleText: true,
      }),
    ).toBe(false);
  });

  it("accepts visible text only after every upload has settled", () => {
    expect(
      canSubmitMessageDraft({
        text: "See the screenshot",
        mentions: [],
        attachments: [attachment("image-1", "image/png")],
        requireVisibleText: true,
      }),
    ).toBe(true);
    expect(
      canSubmitMessageDraft({
        text: "See the screenshot",
        mentions: [],
        attachments: [
          attachment("image-uploading", "image/png", "uploading", undefined),
        ],
        requireVisibleText: true,
      }),
    ).toBe(false);
  });
});

describe("hasUploadingAttachments", () => {
  it("detects an in-flight item without treating failed items as uploading", () => {
    expect(
      hasUploadingAttachments([
        attachment("failed", "image/png", "failed", undefined),
        attachment("active", "application/pdf", "uploading", undefined),
      ]),
    ).toBe(true);
    expect(
      hasUploadingAttachments([
        attachment("failed", "image/png", "failed", undefined),
      ]),
    ).toBe(false);
  });
});
