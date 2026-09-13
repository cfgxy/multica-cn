/**
 * Resolve a server-relative attachment URL against the configured API base.
 *
 * Background: when the backend has no CloudFront signer configured (e.g.
 * the self-hosted RustFS / private-S3 case in MUL-2976), `attachment.url`
 * and `attachment.download_url` come back as server-relative paths like
 * `/api/attachments/{id}/download`. Web is happy with that — same-origin
 * `<img src="/api/...">` resolves against the document base — but RN
 * needs an absolute http(s) URL for both `Linking.openURL` (`Cannot open
 * URL` otherwise) and `<Image source={{ uri }}>` (no document origin to
 * resolve against; the request is silently dropped).
 *
 * Mirrors `packages/core/workspace/avatar-url.ts:resolvePublicFileUrl`
 * exactly. We don't import the core helper because its `getBaseUrl()`
 * pulls from a singleton ApiClient that lives in `@multica/core/api` —
 * not on the mobile sharing whitelist (apps/mobile/CLAUDE.md "mirror,
 * don't import"). Mobile reads the active API base from `server-store`
 * at call time, the same source `data/api.ts` uses — the user can switch
 * servers in-app (RUYI-4), so this must not be bound at module load.
 *
 * Contract:
 *   - null / undefined / "" → null (caller should treat as "no URL").
 *   - already-absolute URL  → returned unchanged.
 *   - server-relative path  → API base + path, with a single boundary
 *                             slash (we trim trailing slashes from the
 *                             base before joining).
 */

import { getApiUrl } from "@/data/server-store";
import { attachmentIdFromDownloadURL } from "@multica/core/types/attachment-url";

export function resolveAttachmentUrlWithBase(
  rawUrl: string | null | undefined,
  baseUrl: string,
): string | null {
  if (!rawUrl) return null;
  if (!rawUrl.startsWith("/")) return rawUrl;
  const trimmedBaseUrl = baseUrl.replace(/\/+$/, "");
  return `${trimmedBaseUrl}${rawUrl}`;
}

export function resolveAttachmentUrl(
  rawUrl: string | null | undefined,
): string | null {
  return resolveAttachmentUrlWithBase(rawUrl, getApiUrl());
}

/**
 * Pick the URL a mobile native image loader (`RNImage.getSize`, `ExpoImage`)
 * can actually fetch for an attachment, in RUYI-141's priority.
 *
 * Background: `download_url` is normally the right first choice — with a
 * CloudFront/S3 signer configured (`h.CFSigner` in
 * `server/internal/handler/file.go`) the server overwrites it with an
 * absolute, credential-free signed URL. But without a signer, both
 * `download_url` and (absent a CDN domain) `markdown_url` render as the
 * *stable* `/api/attachments/{id}/download` path — see `buildMarkdownURL`
 * and `attachmentToResponse` in the same file. That endpoint always calls
 * `requireUserID` (`loadAttachmentForDownload`), so it 401s for a native
 * `<Image>`/`Image.getSize` load that cannot attach an `Authorization`
 * header, and the comment card falls back to a 16:9 grey placeholder.
 *
 * `attachmentIdFromDownloadURL` recognizes that stable-path shape whether
 * the candidate is absolute or server-relative (it only inspects the
 * pathname), which is what lets this skip both the CDN-less `markdown_url`
 * and the un-signed `download_url` while still picking a CloudFront-signed
 * `download_url` first — that URL points at a different host entirely and
 * never matches the shape. `url` — the raw storage URL — is the deployment's
 * anonymously-reachable fallback (RUYI-133 evidence: `/uploads/...` returns
 * 200 without a token where `/api/attachments/{id}/download` returns 401).
 *
 * Shared with `packages/core/attachments/image-sequence.ts`'s
 * `matchAttachmentByURL`: both must resolve the same attachment to the same
 * URL, or the lightbox sequence built from `collectImageSequence` and the
 * URI `MarkdownImage` reports on tap diverge and the viewer can't find its
 * position (see the URI-resolution note atop `markdown-image.tsx`).
 */
export function pickAttachmentImageUrl(attachment: {
  url: string;
  download_url: string;
  markdown_url: string;
}): string {
  const candidates = [
    attachment.download_url,
    attachment.markdown_url,
    attachment.url,
  ];
  const credentialFree = candidates.find(
    (candidate) => candidate && !attachmentIdFromDownloadURL(candidate),
  );
  return (
    credentialFree ||
    attachment.download_url ||
    attachment.markdown_url ||
    attachment.url
  );
}
