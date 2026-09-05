/**
 * Per-server session snapshots and the synchronous server switch, built on
 * the StorageAdapter (desktop token mode keeps the live token in storage
 * under TOKEN_STORAGE_KEY).
 *
 * Switching servers restores the target server's saved session instead of
 * signing the user out — the same semantics the accepted mobile
 * implementation provides via its per-server SecureStore keys. On desktop
 * the snapshots live next to the live token in the same storage adapter;
 * the workspace slug is NOT snapshotted because workspace identity is
 * route-driven on desktop and re-resolves after the switch reloads.
 *
 * All functions are synchronous: the switch ends in window.location.reload()
 * on the caller's side, so there is no async gap in which renderer state
 * could drift from storage.
 */
import type { StorageAdapter } from "../types";
import {
  BUILT_IN_SERVER_ID,
  SERVERS_STORAGE_KEY,
  TOKEN_STORAGE_KEY,
  parsePersistedState,
  serializePersistedState,
} from "./server-config";

/** Storage key for one server's session snapshot. */
export function serverTokenKey(serverId: string): string {
  return `multica_server_token_${serverId}`;
}

/**
 * Snapshot the live token under the outgoing server's key. A missing live
 * token (already logged out) clears the snapshot instead of writing an
 * empty value, so a stale session can never resurrect.
 */
export function captureActiveServerToken(
  storage: StorageAdapter,
  serverId: string,
): void {
  const token = storage.getItem(TOKEN_STORAGE_KEY);
  if (token) storage.setItem(serverTokenKey(serverId), token);
  else storage.removeItem(serverTokenKey(serverId));
}

/**
 * Restore the target server's snapshot into the live token key. No
 * snapshot means the target has no saved session — the live key is removed
 * so boot lands on the login page, matching the cold-start behavior.
 */
export function activateServerToken(
  storage: StorageAdapter,
  serverId: string,
): void {
  const token = storage.getItem(serverTokenKey(serverId));
  if (token) storage.setItem(TOKEN_STORAGE_KEY, token);
  else storage.removeItem(TOKEN_STORAGE_KEY);
}

/** Drop one server's session snapshot (used when the entry is deleted). */
export function clearServerToken(
  storage: StorageAdapter,
  serverId: string,
): void {
  storage.removeItem(serverTokenKey(serverId));
}

/**
 * Switch the active server entirely at the storage level. Order follows
 * the accepted mobile semantics: persist the new active id FIRST (a
 * failure here leaves the user on the current server untouched), then
 * swap the session snapshots. The caller reloads the window on `true`.
 *
 * `targetId` is valid when it names the built-in entry (which never
 * persists, so it is matched by id — mobile validates against the
 * composed list for the same reason) or one of the persisted custom
 * entries. Returns false for anything else — nothing is written.
 */
export function switchActiveServerSync(
  storage: StorageAdapter,
  targetId: string,
  builtInId: string = BUILT_IN_SERVER_ID,
): boolean {
  const persisted = parsePersistedState(
    storage.getItem(SERVERS_STORAGE_KEY),
  );
  const customServers = persisted?.servers ?? [];
  const knownTarget =
    targetId === builtInId ||
    customServers.some((s) => s.id === targetId);
  if (!knownTarget) return false;

  const currentId = persisted?.activeServerId ?? builtInId;
  // 1. Persist the new active id.
  storage.setItem(
    SERVERS_STORAGE_KEY,
    serializePersistedState(customServers, targetId),
  );
  // 2. Capture the outgoing server's session.
  captureActiveServerToken(storage, currentId);
  // 3. Restore the target server's session.
  activateServerToken(storage, targetId);
  return true;
}

/**
 * Invalidate the active server's session — desktop's counterpart of the
 * mobile "editing the active server's address signs the user out" rule:
 * the live token was minted by the old address, so both the live key and
 * that server's snapshot must go before the app reloads into the login
 * page.
 */
export function resetActiveServerSession(storage: StorageAdapter): void {
  const persisted = parsePersistedState(
    storage.getItem(SERVERS_STORAGE_KEY),
  );
  const currentId = persisted?.activeServerId ?? BUILT_IN_SERVER_ID;
  storage.removeItem(TOKEN_STORAGE_KEY);
  clearServerToken(storage, currentId);
}
