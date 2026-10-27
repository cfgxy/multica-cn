// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  attachmentPreviewPath,
  encodeAttachmentBytesForDom,
  getMobileAttachmentPreviewKind,
  resolveMobileAttachmentPreviewUrl,
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

  it("uses the inline capability rather than the forced-download sibling", () => {
    const resolveUrl = (url: string | null | undefined) => url ? `https://api.example.test${url}` : null;
    expect(resolveMobileAttachmentPreviewUrl({
      download_url: "/attachments/a?sig=inline",
      attachment_download_url: "/attachments/a?sig=download&dl=1",
    }, resolveUrl)).toBe("https://api.example.test/attachments/a?sig=inline");
  });

  it("encodes native-fetched bytes for the DOM bridge", () => {
    expect(encodeAttachmentBytesForDom(new Uint8Array([0x25, 0x50, 0x44, 0x46]))).toBe("JVBERg==");
  });
});
