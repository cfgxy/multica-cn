// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  attachmentPreviewPath,
  getMobileAttachmentPreviewKind,
} from "./attachment-preview";

describe("mobile attachment preview dispatch", () => {
  it("routes Markdown and PDF by MIME or filename extension", () => {
    expect(getMobileAttachmentPreviewKind("text/markdown; charset=utf-8", "notes.txt")).toBe("markdown");
    expect(getMobileAttachmentPreviewKind("application/octet-stream", "notes.MD")).toBe("markdown");
    expect(getMobileAttachmentPreviewKind("application/pdf", "document.bin")).toBe("pdf");
    expect(getMobileAttachmentPreviewKind("", "document.PDF")).toBe("pdf");
  });

  it("keeps other file types on the explicit download fallback", () => {
    expect(getMobileAttachmentPreviewKind("application/zip", "archive.zip")).toBe("unsupported");
  });

  it("maps every supported source to the same attachment-id reader route", () => {
    expect(attachmentPreviewPath("team-space", "attachment-123")).toBe(
      "/team-space/attachment/attachment-123",
    );
  });
});
