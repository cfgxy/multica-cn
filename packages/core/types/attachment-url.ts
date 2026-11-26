/**
 * Stable URL helpers for attachment references that get persisted into
 * markdown bodies (issue descriptions, comment bodies, chat messages).
 *
 * Background — MUL-3130:
 *
 *   The original upload flow returned `att.url` as the link to embed in
 *   markdown. After PR #3903 (MUL-3132), `att.url` for the LocalStorage
 *   backend became a 30-minute HMAC-signed `/uploads/<key>?exp&sig` URL.
 *   That URL is short-lived by design — it's the in-memory token that
 *   keeps native browser <img>/<video> resource loads working without an
 *   Authorization header. Persisting it into a comment body means the
 *   comment's images break the moment the signature expires (default
 *   30 min). The same problem applies to CloudFront-mode `download_url`
 *   values, which are also signed redirects.
 *
 *   The fix is to persist a STABLE path that contains only the
 *   attachment id. The server's /api/attachments/{id}/download endpoint
 *   self-resolves the workspace from the attachment row and re-signs (or
 *   proxies) on every request, so the URL is correct forever — until
 *   the attachment row itself is deleted.
 */

const DOWNLOAD_PREFIX = "/api/attachments/";
const DOWNLOAD_SUFFIX = "/download";
const ATTACHMENT_PROTOCOL_PREFIX = "attachment://";
const CHANNEL_MEDIA_MARKER_RE =
  /<!-- multica:channel-media:[0-9a-fA-F-]{36} -->/g;

/** Remove server-owned channel-media merge metadata from editable or rendered
 * Markdown while leaving the visible attachment link untouched. */
export function stripChannelMediaMarkers(content: string): string {
  return content.replace(CHANNEL_MEDIA_MARKER_RE, "");
}

/**
 * UUID literal regex (RFC 4122 form). Used to extract an attachment id
 * out of a stable download URL when matching markdown image refs back
 * to their attachment record. Anchored to the segment between
 * `/api/attachments/` and `/download` so we never accept a non-uuid
 * path component as an id.
 */
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface ParsedAttachmentProtocolTarget {
  id?: string;
  filename: string;
  rawTarget: string;
}

export function isAttachmentProtocolURL(rawURL: string): boolean {
  return /^attachment:\/\//i.test((rawURL ?? "").trim());
}

function safeDecodeURIComponent(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export function normalizeAttachmentFilename(
  filename: string | null | undefined,
): string {
  const normalized = (filename ?? "")
    .trim()
    .replace(/\\/g, "/")
    .replace(/^\/+/, "");
  if (!normalized) return "";
  const parts: string[] = [];
  for (const part of normalized.split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") return "";
    parts.push(part);
  }
  return parts.join("/");
}

export function attachmentFilenameBasename(
  filename: string | null | undefined,
): string {
  const normalized = normalizeAttachmentFilename(filename);
  if (!normalized) return "";
  const idx = normalized.lastIndexOf("/");
  return idx >= 0 ? normalized.slice(idx + 1) : normalized;
}

/**
 * Parse an `attachment://<id|filename|relpath>` URL into a normalized target.
 * Strips query/fragment, safely URL-decodes, normalizes `./` and `\`, and
 * rejects empty targets or `..` traversal segments.
 */
export function parseAttachmentProtocolTarget(
  rawURL: string,
): ParsedAttachmentProtocolTarget | undefined {
  const trimmed = (rawURL ?? "").trim();
  if (!isAttachmentProtocolURL(trimmed)) return undefined;

  const withoutPrefix = trimmed.slice(ATTACHMENT_PROTOCOL_PREFIX.length);
  const withoutQueryOrFragment = stripQueryAndFragment(withoutPrefix);
  const decoded = safeDecodeURIComponent(withoutQueryOrFragment);
  const normalized = normalizeAttachmentFilename(decoded);
  if (!normalized) return undefined;

  const filename = attachmentFilenameBasename(normalized);
  if (!filename) return undefined;

  return {
    id: UUID_RE.test(normalized) ? normalized.toLowerCase() : undefined,
    filename,
    rawTarget: normalized,
  };
}

export function attachmentMatchesProtocolURL(
  rawURL: string,
  attachment: { id: string; filename?: string | null },
): boolean {
  const parsed = parseAttachmentProtocolTarget(rawURL);
  if (!parsed) return false;
  if (parsed.id) {
    return parsed.id === attachment.id.toLowerCase();
  }
  if (!attachment.filename) return false;
  const attFull = normalizeAttachmentFilename(attachment.filename);
  const attBase = attachmentFilenameBasename(attachment.filename);
  if (!attFull && !attBase) return false;
  return (
    parsed.rawTarget === attFull ||
    parsed.rawTarget === attBase ||
    parsed.filename === attBase
  );
}

function blankOutPreservingNewlines(text: string): string {
  return text.replace(/[^\r\n]/g, " ");
}

/**
 * Blank fenced code blocks and inline code spans while preserving offsets and
 * newlines. Linear scanner (no backreferenced regexes) so agent-authored
 * markdown cannot trigger ReDoS.
 */
export function maskMarkdownCode(content: string): string {
  if (!content) return "";
  const lines = content.split("\n");
  let fence: string | null = null;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!;
    if (fence !== null) {
      lines[i] = blankOutPreservingNewlines(line);
      const closeMatch = /^[ \t]{0,3}(`{3,}|~{3,})[ \t]*\r?$/.exec(line);
      if (
        closeMatch !== null &&
        closeMatch[1]![0] === fence[0] &&
        closeMatch[1]!.length >= fence.length
      ) {
        fence = null;
      }
      continue;
    }
    const openMatch = /^[ \t]{0,3}(`{3,}|~{3,})([^\r\n]*)\r?$/.exec(line);
    if (openMatch !== null) {
      const marker = openMatch[1]!;
      const info = openMatch[2] ?? "";
      // CommonMark: backtick fence info strings cannot contain backticks; a
      // line like ```foo``` is an inline code span, not a fenced block.
      if (marker[0] !== "`" || !info.includes("`")) {
        fence = marker;
        lines[i] = blankOutPreservingNewlines(line);
        continue;
      }
    }
  }

  const fenceMasked = lines.join("\n");
  if (!fenceMasked.includes("`")) return fenceMasked;

  const chars = fenceMasked.split("");
  let i = 0;
  while (i < chars.length) {
    if (chars[i] !== "`") {
      i++;
      continue;
    }
    let openLen = 0;
    while (i + openLen < chars.length && chars[i + openLen] === "`") {
      openLen++;
    }
    let j = i + openLen;
    let foundClose = -1;
    while (j < chars.length) {
      if (
        chars[j] === "\n" &&
        (chars[j + 1] === "\n" || (chars[j + 1] === "\r" && chars[j + 2] === "\n"))
      ) {
        // Inline code spans cannot cross a blank line (paragraph boundary).
        break;
      }
      if (chars[j] === "`") {
        let closeLen = 0;
        while (j + closeLen < chars.length && chars[j + closeLen] === "`") {
          closeLen++;
        }
        if (closeLen === openLen) {
          foundClose = j + closeLen;
          break;
        }
        j += closeLen;
        continue;
      }
      j++;
    }
    if (foundClose !== -1) {
      for (let k = i; k < foundClose; k++) {
        if (chars[k] !== "\n" && chars[k] !== "\r") chars[k] = " ";
      }
      i = foundClose;
    } else {
      i += openLen;
    }
  }
  return chars.join("");
}

/**
 * Build the stable per-attachment download URL — the only attachment
 * URL shape that is safe to persist into markdown bodies.
 *
 * Returns a site-relative path (no host) so the same value works in
 * SSR, CSR, Electron, and the mobile webview where the host differs.
 *
 * Callers SHOULD use this when emitting markdown that references an
 * attachment (image src, file-card href, attachment_ids tracker keys).
 * For ad-hoc one-off URLs that don't outlive the current session
 * (e.g. a download click handler that re-signs the URL on every press)
 * the server-returned `download_url` is still appropriate.
 */
export function attachmentDownloadPath(attachmentId: string): string {
  return `${DOWNLOAD_PREFIX}${attachmentId}${DOWNLOAD_SUFFIX}`;
}

/**
 * Extract the attachment id from a `/api/attachments/<uuid>/download`
 * URL (with or without query/host). Returns `undefined` if the URL is
 * not a download URL, or if the captured segment is not a valid UUID
 * literal.
 *
 * The renderer uses this to map a markdown image ref back to its
 * attachment record so it can swap in CloudFront-signed URLs for
 * faster fetches when configured, surface preview metadata, and keep
 * the standalone-attachment dedup logic working when the markdown
 * uses a stable path instead of the storage URL.
 */
export function attachmentIdFromDownloadURL(rawURL: string): string | undefined {
  if (!rawURL) return undefined;

  // Strip query string + fragment so a `?exp=...` (legacy signed URL
  // accidentally embedded by an old client) does not poison the match.
  let path = rawURL;
  const qi = path.indexOf("?");
  if (qi >= 0) path = path.slice(0, qi);
  const hi = path.indexOf("#");
  if (hi >= 0) path = path.slice(0, hi);

  // Allow absolute URLs (electron, host-bearing CDN URL); pull just the
  // pathname. Skip the protocol-aware parser for site-relative paths so
  // SSR (no document) and webview environments behave identically.
  if (/^https?:\/\//i.test(path)) {
    try {
      path = new URL(path).pathname;
    } catch {
      return undefined;
    }
  }

  if (!path.startsWith(DOWNLOAD_PREFIX)) return undefined;
  if (!path.endsWith(DOWNLOAD_SUFFIX)) return undefined;

  const id = path.slice(DOWNLOAD_PREFIX.length, path.length - DOWNLOAD_SUFFIX.length);
  if (!UUID_RE.test(id)) return undefined;
  return id;
}

function stripQueryAndFragment(url: string): string {
  return url.split(/[?#]/, 1)[0] ?? "";
}

function contentReferencesURL(content: string, url?: string): boolean {
  if (!url) return false;
  if (content.includes(url)) return true;
  const stable = stripQueryAndFragment(url);
  return stable !== "" && content.includes(stable);
}

const MARKDOWN_ATTACHMENT_PROTOCOL_RE =
  /!?\[(?:\\.|[^\]\\\n])*\]\(\s*(?:<(attachment:\/\/[^>\r\n]+)>|(attachment:\/\/(?:[^()\s]+|\([^()\s]*\))+))[^)]*\)/gi;
const HTML_ATTACHMENT_PROTOCOL_RE =
  /<(?:img|a)\b[^>]*?\s(?:src|href)\s*=\s*(?:"(attachment:\/\/[^"]*)"|'(attachment:\/\/[^']*)'|(attachment:\/\/[^\s"'>]+))[^>]*>/gi;
const AUTOLINK_ATTACHMENT_PROTOCOL_RE = /<(attachment:\/\/[^\s<>]+)>/gi;

function extractAttachmentProtocolRefs(content: string): string[] {
  const masked = maskMarkdownCode(content);
  if (!/attachment:\/\//i.test(masked)) return [];

  const refs: string[] = [];
  for (const m of masked.matchAll(MARKDOWN_ATTACHMENT_PROTOCOL_RE)) {
    const url = (m[1] ?? m[2] ?? "").trim();
    if (url) refs.push(url);
  }
  for (const m of masked.matchAll(HTML_ATTACHMENT_PROTOCOL_RE)) {
    const url = (m[1] ?? m[2] ?? m[3] ?? "").trim();
    if (url) refs.push(url);
  }
  for (const m of masked.matchAll(AUTOLINK_ATTACHMENT_PROTOCOL_RE)) {
    const url = (m[1] ?? "").trim();
    if (url) refs.push(url);
  }
  return refs;
}

/**
 * True when `content` contains a markdown reference to `attachment` —
 * either the new stable `/api/attachments/<id>/download` shape, the legacy
 * `att.url` storage path, or a non-code `attachment://<id|filename>` ref.
 * Used by the comment composer and the standalone-attachment list to decide
 * whether an attachment is "in the body" (and therefore should be tracked as
 * a comment attachment but not rendered as a card below).
 */
export function contentReferencesAttachment(
  content: string,
  attachment: {
    id: string;
    url: string;
    filename?: string;
    download_url?: string;
    markdown_url?: string;
  },
): boolean {
  if (!content) return false;
  if (content.includes(attachmentDownloadPath(attachment.id))) return true;
  if (contentReferencesURL(content, attachment.url)) return true;
  if (contentReferencesURL(content, attachment.download_url)) return true;
  if (contentReferencesURL(content, attachment.markdown_url)) return true;
  for (const refURL of extractAttachmentProtocolRefs(content)) {
    if (attachmentMatchesProtocolURL(refURL, attachment)) return true;
  }
  return false;
}
