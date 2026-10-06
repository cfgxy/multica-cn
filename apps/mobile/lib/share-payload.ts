/**
 * Trust boundary between the OS share pipeline (Android ACTION_SEND /
 * ACTION_SEND_MULTIPLE via modules/share-intent) and the app. The native
 * side copies stream URIs into our cache dir before handing them over; this
 * parser re-validates that hand-off so everything downstream (share landing
 * page, attachment zone, upload) can assume well-formed file:// URIs and
 * display-safe names.
 *
 * No RN / Expo imports on purpose: this module runs under vitest's Node
 * environment (mobile tests are pure-logic only).
 */

/** Hard cap on files accepted from one share — mirrors the native side. */
export const MAX_SHARE_FILES = 20;

/** Display-name cap; longer names truncate keeping the extension. */
export const MAX_SHARED_NAME_LENGTH = 200;

/** One file handed over by the native share module (already copied to cache). */
export interface SharedFile {
  /** file:// URI of the cache copy — safe to read for the process lifetime. */
  uri: string;
  name: string;
  mimeType: string;
  size?: number;
}

/** Shape the native module returns (loose on purpose — it crosses the bridge). */
export interface RawShareFile {
  uri?: unknown;
  name?: unknown;
  mimeType?: unknown;
  size?: unknown;
}

const DEFAULT_MIME = "application/octet-stream";
/** Path separators, Windows-reserved characters and control characters —
 *  the display name is never used as a path component client-side, but the
 *  upload filename goes into multipart Content-Disposition and server-side
 *  storage; a sanitized name keeps every consumer honest. */
const UNSAFE_NAME_CHARS = /[/\\:*?"<>|\u0000-\u001f]/g;

function asNonEmptyString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function sanitizeName(raw: string): string {
  const cleaned = raw.replace(UNSAFE_NAME_CHARS, "_").trim();
  if (cleaned.length === 0) return "shared-file";
  if (cleaned.length <= MAX_SHARED_NAME_LENGTH) return cleaned;
  // Keep the tail so the extension survives the truncation.
  return cleaned.slice(cleaned.length - MAX_SHARED_NAME_LENGTH);
}

function asSize(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
    ? value
    : undefined;
}

function normalizeFile(raw: unknown): SharedFile | null {
  if (typeof raw !== "object" || raw === null) return null;
  const candidate = raw as RawShareFile;
  const uri = asNonEmptyString(candidate.uri);
  const name = asNonEmptyString(candidate.name);
  // Only accept the cache copies the native module produced — content://
  // grants are transient and would break once the share intent dies.
  if (!uri || !name || !uri.startsWith("file://")) return null;
  return {
    uri,
    name: sanitizeName(name),
    mimeType: asNonEmptyString(candidate.mimeType) ?? DEFAULT_MIME,
    size: asSize(candidate.size),
  };
}

export interface SharePayload {
  files: SharedFile[];
}

/**
 * Validate a native share hand-off. Returns null when the payload carries no
 * usable file (empty share, text-only share, or bridge garbage) — callers
 * must treat null as "no share to land on".
 */
export function normalizeSharePayload(raw: unknown): SharePayload | null {
  if (typeof raw !== "object" || raw === null) return null;
  const files = (raw as { files?: unknown }).files;
  if (!Array.isArray(files)) return null;
  const normalized: SharedFile[] = [];
  for (const entry of files) {
    const file = normalizeFile(entry);
    if (file) normalized.push(file);
    if (normalized.length >= MAX_SHARE_FILES) break;
  }
  if (normalized.length === 0) return null;
  return { files: normalized };
}
