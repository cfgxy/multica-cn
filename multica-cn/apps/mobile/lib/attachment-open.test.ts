/**
 * RUYI-73 — credential-free attachment open/download on mobile.
 *
 * Regression shape: the file card (and inline attachment links) used to
 * hand the LIST-response `download_url` — the auth-gated stable
 * `/api/attachments/{id}/download` in proxy mode — to `Linking.openURL`.
 * The system browser carries neither the app's Bearer token nor a session
 * cookie, so the server answered 401 and the phone displayed the raw
 * `{"error":"missing authorization"}` JSON body. The fix re-signs at tap
 * time through GET /api/attachments/{id} (the same click-time re-sign
 * web's use-download-attachment performs) and opens the freshly minted
 * credential-free URL.
 *
 * The flow functions take their collaborators (metadata source, URL
 * opener, resolver) as parameters — same injectable-opener idiom as
 * `pull-request-link.ts` — so this suite stays on the node-only vitest
 * lane with plain fakes and no react-native / fetch mocking.
 */
import { describe, expect, it, vi } from "vitest";

import {
  attachmentIdFromUrl,
  openAttachmentDownload,
  type AttachmentMetadataSource,
  type URLOpener,
  type UrlResolver,
} from "./attachment-open";

const UUID = "0b9e6c1a-7c47-4e0f-9a55-3f1b2d4e5a60";

/** Identity resolver — passes absolute URLs through, maps server-relative
 *  paths to a marker absolute form so assertions can tell them apart. */
const resolveIdentity: UrlResolver = (raw) => {
  if (!raw) return null;
  return raw.startsWith("/") ? `https://api.test${raw}` : raw;
};

function makeSource(
  impl: (id: string) => Promise<{ attachment_download_url?: string; download_url: string }>,
): AttachmentMetadataSource & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    getAttachment: vi.fn(async (id: string) => {
      calls.push(id);
      return impl(id);
    }),
  };
}

function makeOpener(): URLOpener & { opened: string[]; failNextWith?: Error } {
  const opened: string[] = [];
  return {
    opened,
    openURL: vi.fn(async (url: string) => {
      opened.push(url);
    }),
  };
}

describe("attachmentIdFromUrl", () => {
  it("extracts the id from a persisted site-relative download path", () => {
    expect(attachmentIdFromUrl(`/api/attachments/${UUID}/download`)).toBe(UUID);
  });

  it("extracts the id from an absolute URL carrying the download path", () => {
    expect(
      attachmentIdFromUrl(`https://multica.example.com/api/attachments/${UUID}/download`),
    ).toBe(UUID);
  });

  it("extracts the id from a signed-download capability URL with query", () => {
    expect(
      attachmentIdFromUrl(`/api/attachments/${UUID}/signed-download?exp=123&sig=abc`),
    ).toBe(UUID);
  });

  it("extracts the id from an mc://file reference", () => {
    expect(attachmentIdFromUrl(`mc://file/${UUID}`)).toBe(UUID);
  });

  it("rejects non-attachment and malformed shapes", () => {
    expect(attachmentIdFromUrl("/uploads/some/key.png")).toBeNull();
    expect(attachmentIdFromUrl("https://github.com/owner/repo")).toBeNull();
    expect(attachmentIdFromUrl("mention://issue/not-a-uuid")).toBeNull();
    expect(attachmentIdFromUrl("/api/attachments/not-a-uuid/download")).toBeNull();
    expect(attachmentIdFromUrl("")).toBeNull();
  });
});

describe("openAttachmentDownload", () => {
  it("mints at tap time and opens the forced-attachment URL", async () => {
    const source = makeSource(async () => ({
      download_url: `/api/attachments/${UUID}/signed-download?exp=1&sig=load`,
      attachment_download_url: `/api/attachments/${UUID}/signed-download?exp=1&sig=dl&dl=1`,
    }));
    const opener = makeOpener();

    const result = await openAttachmentDownload(UUID, {
      source,
      opener,
      resolveUrl: resolveIdentity,
      fallbackUrl: `/api/attachments/${UUID}/download`,
    });

    expect(result).toEqual({ ok: true });
    expect(source.calls).toEqual([UUID]);
    expect(opener.opened).toEqual([
      `https://api.test/api/attachments/${UUID}/signed-download?exp=1&sig=dl&dl=1`,
    ]);
  });

  it("falls back to the load-intent URL when no forced URL was minted", async () => {
    const source = makeSource(async () => ({
      download_url: `/api/attachments/${UUID}/signed-download?exp=1&sig=load`,
    }));
    const opener = makeOpener();

    const result = await openAttachmentDownload(UUID, {
      source,
      opener,
      resolveUrl: resolveIdentity,
    });

    expect(result).toEqual({ ok: true });
    expect(opener.opened).toEqual([
      `https://api.test/api/attachments/${UUID}/signed-download?exp=1&sig=load`,
    ]);
  });

  it("reopens the caller-supplied URL when the mint fails", async () => {
    const source = makeSource(async () => {
      throw new Error("boom");
    });
    const opener = makeOpener();

    const result = await openAttachmentDownload(UUID, {
      source,
      opener,
      resolveUrl: resolveIdentity,
      fallbackUrl: `/api/attachments/${UUID}/download`,
    });

    expect(result).toEqual({ ok: true });
    expect(opener.opened).toEqual([`https://api.test/api/attachments/${UUID}/download`]);
  });

  it("reports failure without opening when mint fails and no fallback exists", async () => {
    const source = makeSource(async () => {
      throw new Error("boom");
    });
    const opener = makeOpener();

    const result = await openAttachmentDownload(UUID, {
      source,
      opener,
      resolveUrl: resolveIdentity,
    });

    expect(result.ok).toBe(false);
    expect(opener.opened).toEqual([]);
  });

  it("surfaces opener failures as a typed result", async () => {
    const source = makeSource(async () => ({
      download_url: `/api/attachments/${UUID}/signed-download?exp=1&sig=load`,
    }));
    const opener = makeOpener();
    opener.openURL = vi.fn(async () => {
      throw new Error("Cannot open URL");
    });

    const result = await openAttachmentDownload(UUID, {
      source,
      opener,
      resolveUrl: resolveIdentity,
    });

    expect(result).toEqual({ ok: false, message: "Cannot open URL" });
  });

  it("reports failure when neither mint nor fallback yields a URL", async () => {
    const source = makeSource(async () => ({ download_url: "" }));
    const opener = makeOpener();

    const result = await openAttachmentDownload(UUID, {
      source,
      opener,
      resolveUrl: resolveIdentity,
    });

    expect(result.ok).toBe(false);
    expect(opener.opened).toEqual([]);
  });
});
