/**
 * Credential-free attachment open/download (RUYI-73).
 *
 * Bug shape: file cards and inline attachment links used to hand the
 * LIST-response `download_url` straight to `Linking.openURL`. In proxy
 * mode (local disk / private object storage — the self-hosted default)
 * that URL is the auth-gated stable `/api/attachments/{id}/download`
 * path, and a system-browser hand-off carries neither the app's Bearer
 * token (it lives in JS) nor a session cookie (mobile is token-mode, and
 * the auth cookie is SameSite=Strict anyway) — the server answered 401
 * and the phone displayed the raw `{"error":"missing authorization"}`
 * JSON body. Persisted `!file` markdown links had a second failure mode:
 * the href is server-relative, and `Linking.openURL` throws on relative
 * URLs, so the tap silently did nothing.
 *
 * The fix mirrors what web's `use-download-attachment.ts` does at click
 * time: GET /api/attachments/{id} (authenticated — the mobile ApiClient
 * rides Bearer + X-Workspace-Slug on every call) returns freshly minted,
 * credential-free URLs for THIS response in every storage mode — a
 * 60-second single-attachment signed capability in proxy mode, or an
 * absolute presigned / CloudFront URL otherwise. The forced-attachment
 * URL (`attachment_download_url`, `dl=1`) is preferred so the system
 * viewer saves the file instead of previewing it inline; the load-intent
 * `download_url` is the fallback for servers predating that field.
 *
 * When the mint fails (offline, stale token, 404) the caller-supplied
 * `fallbackUrl` is opened best-effort — the pre-RUYI-73 behavior — which
 * still works for a user whose system browser holds a same-origin web
 * session cookie, and degrades to the old symptom instead of a dead tap.
 *
 * Collaborators are injected (`source` / `opener` / `resolveUrl`) — same
 * idiom as `pull-request-link.ts` — so the flow runs on the node-only
 * vitest lane with plain fakes.
 */

export interface URLOpener {
  openURL(url: string): Promise<unknown>;
}

/** Minimal surface of the mobile ApiClient this flow needs. Structural:
 *  the `api` singleton satisfies it; tests pass fakes. */
export interface AttachmentMetadataSource {
  getAttachment(
    attachmentId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<{ attachment_download_url?: string; download_url: string }>;
}

/** Server-relative → absolute URL resolution (`resolveAttachmentUrl`). */
export type UrlResolver = (raw: string | null | undefined) => string | null;

export type OpenAttachmentResult = { ok: true } | { ok: false; message: string };

const ATTACHMENT_UUID =
  "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}";

// Matches every URL shape that names an attachment by id, site-relative or
// absolute, with or without capability query params:
//   /api/attachments/<id>/download            (persisted markdown / list URL)
//   https://host/api/attachments/<id>/download  (`markdown_url` with PUBLIC_URL)
//   /api/attachments/<id>/signed-download?…   (capability URLs)
const ATTACHMENT_URL_RE = new RegExp(
  `/api/attachments/(${ATTACHMENT_UUID})/(?:download|signed-download)(?:[?#]|$)`,
  "i",
);

// The canonical reference shape the upload response carries (`att.url` in
// proxy mode) — also accepted so an inline `[name](mc://file/<id>)` link
// routes through the same mint flow instead of a dead Linking hand-off.
const MC_FILE_RE = new RegExp(`^mc://file/(${ATTACHMENT_UUID})$`, "i");

/** Extract the attachment id from any URL shape that names one, or null
 *  for non-attachment URLs (external links, `/uploads/*`, mentions). */
export function attachmentIdFromUrl(url: string): string | null {
  if (!url) return null;
  const trimmed = url.trim();
  return (
    ATTACHMENT_URL_RE.exec(trimmed)?.[1] ?? MC_FILE_RE.exec(trimmed)?.[1] ?? null
  );
}

/**
 * Open one attachment in the system viewer with a freshly minted,
 * credential-free URL. Resolves to a typed result so screens can give
 * feedback without try/catch.
 */
export async function openAttachmentDownload(
  attachmentId: string,
  deps: {
    source: AttachmentMetadataSource;
    opener: URLOpener;
    resolveUrl: UrlResolver;
    /** URL to fall back to when the mint fails — usually the list-response
     *  `download_url` (or raw href) the caller already holds. */
    fallbackUrl?: string | null;
  },
): Promise<OpenAttachmentResult> {
  let target: string | null = null;
  try {
    const fresh = await deps.source.getAttachment(attachmentId);
    target = deps.resolveUrl(fresh.attachment_download_url || fresh.download_url);
  } catch {
    target = deps.resolveUrl(deps.fallbackUrl);
  }
  if (!target) {
    return { ok: false, message: "No downloadable URL for this attachment." };
  }
  try {
    await deps.opener.openURL(target);
    return { ok: true };
  } catch (err) {
    return {
      ok: false,
      message:
        err instanceof Error ? err.message : "Could not open the attachment.",
    };
  }
}
