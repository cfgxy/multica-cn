// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  attachmentPreviewPath,
  getMobileAttachmentPreviewKind,
  resolveMobileAttachmentPreviewUrl,
} from "./attachment-preview";

const API_ORIGIN = "https://api.example.test";
const resolveUrl = (rawUrl: string | null | undefined) =>
  rawUrl?.startsWith("/") ? `${API_ORIGIN}${rawUrl}` : rawUrl ?? null;

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

  it("resolves a relative signed capability URL before passing it to the PDF DOM", () => {
    expect(
      resolveMobileAttachmentPreviewUrl(
        {
          download_url: "/api/attachments/att-1/signed-download?exp=1&sig=load",
          attachment_download_url:
            "/api/attachments/att-1/signed-download?exp=1&sig=download&dl=1",
        },
        resolveUrl,
      ),
    ).toBe("https://api.example.test/api/attachments/att-1/signed-download?exp=1&sig=load");
  });

  it("uses the load-intent capability for private proxy storage, not its forced-download sibling", () => {
    expect(
      resolveMobileAttachmentPreviewUrl(
        {
          download_url: "/api/attachments/att-2/signed-download?exp=2&sig=inline",
          attachment_download_url:
            "/api/attachments/att-2/signed-download?exp=2&sig=download&dl=1",
        },
        resolveUrl,
      ),
    ).toBe(
      "https://api.example.test/api/attachments/att-2/signed-download?exp=2&sig=inline",
    );
  });

  it("preserves an absolute presigned or CDN URL for inline preview", () => {
    const signedUrl = "https://cdn.example.test/files/att-3.pdf?Policy=p&Signature=s";
    expect(
      resolveMobileAttachmentPreviewUrl({ download_url: signedUrl }, resolveUrl),
    ).toBe(signedUrl);
  });
});
