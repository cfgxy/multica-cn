/**
 * Deduplication cursor for system notifications (RUYI-37).
 *
 * Remembers which inbox item ids have already produced a status-bar
 * notification so the same message never notifies twice across the three
 * delivery states:
 *   - foreground   — each `inbox:new` WS frame checks the cursor once
 *   - background   — the WS is paused (realtime-provider), no frames, no
 *                    notifications; on resume the cursor simply has no
 *                    duplicate to suppress
 *   - cold start   — the cursor is restored from AsyncStorage before any
 *                    frame can arrive, so history cannot re-notify
 *
 * Notifications are driven ONLY by live `inbox:new` frames — never by the
 * inbox list query — so there is exactly one producer to gate. The cost is
 * the known v1 boundary (server push is a follow-up issue): messages that
 * arrive while the WS is down surface as inbox unread dots, not as
 * notifications.
 *
 * Pure logic here; AsyncStorage binding lives in the realtime hook, which
 * keeps this file vitest-loadable (mirrors the session-keys.ts split).
 *
 * Capacity bounds storage growth. Items are created once and never become
 * "new" again, so an id older than 500 newer ones is unreifiable anyway.
 * Ids are UUIDv7s (globally unique), so the cursor needs no per-workspace
 * scoping — the storage KEY is per server+user, which is what isolates
 * accounts (see cursorStorageKey).
 */

export const NOTIFIED_CURSOR_CAPACITY = 500;

export interface NotifiedCursor {
  /** Most recently recorded first; oldest evicted past capacity. */
  ids: string[];
}

export function emptyNotifiedCursor(): NotifiedCursor {
  return { ids: [] };
}

export function cursorHas(cursor: NotifiedCursor, id: string): boolean {
  return cursor.ids.includes(id);
}

/** Record ids (in arrival order). Returns a new cursor; input untouched. */
export function cursorRecord(
  cursor: NotifiedCursor,
  newIds: readonly string[],
): NotifiedCursor {
  const seen = new Set(cursor.ids);
  const batch = newIds.filter((id) => !seen.has(id));
  if (batch.length === 0) return cursor;
  // Last-arrived first, so a capacity eviction drops the oldest arrival.
  const merged = [...batch.reverse(), ...cursor.ids];
  return { ids: merged.slice(0, NOTIFIED_CURSOR_CAPACITY) };
}

export function cursorStorageKey(serverId: string, userId: string): string {
  return `multica_notified_inbox_ids:${serverId}:${userId}`;
}

const CURSOR_FORMAT_VERSION = 1;

export function serializeNotifiedCursor(cursor: NotifiedCursor): string {
  return JSON.stringify({ v: CURSOR_FORMAT_VERSION, ids: cursor.ids });
}

export function parseNotifiedCursor(
  raw: string | null | undefined,
): NotifiedCursor {
  if (!raw) return emptyNotifiedCursor();
  try {
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null) {
      return emptyNotifiedCursor();
    }
    const ids = (parsed as { ids?: unknown }).ids;
    if (!Array.isArray(ids)) return emptyNotifiedCursor();
    const clean = ids.filter((id): id is string => typeof id === "string");
    return { ids: clean.slice(0, NOTIFIED_CURSOR_CAPACITY) };
  } catch {
    return emptyNotifiedCursor();
  }
}
