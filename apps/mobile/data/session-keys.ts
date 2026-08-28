/**
 * Per-server session key derivation and legacy migration planning — pure
 * logic, no native imports (vitest can't load expo-secure-store; the
 * native-touching wrapper lives in secure-storage.ts, mirroring the
 * server-config.ts / server-store.ts split).
 *
 * Sessions are snapshotted per server entry: switching servers restores the
 * target server's saved token + workspace slug instead of signing out. The
 * keys are derived from ServerEntry.id (stable across rename/URL edits).
 *
 * History: before per-server snapshots the token lived at the single global
 * key "multica_token" and the workspace slug at "multica_current_workspace_slug"
 * (matching web/desktop "multica_token" naming). Upgrades migrate those once
 * to the then-active server's scoped keys.
 */

export const LEGACY_TOKEN_KEY = "multica_token";
export const LEGACY_SLUG_KEY = "multica_current_workspace_slug";

export function tokenKeyFor(serverId: string): string {
  return `multica_token:${serverId}`;
}

export function slugKeyFor(serverId: string): string {
  return `multica_workspace_slug:${serverId}`;
}

export interface LegacyMigrationInput {
  legacyToken: string | null;
  legacySlug: string | null;
  /** Already-stored values under the active server's scoped keys. */
  scopedToken: string | null;
  scopedSlug: string | null;
}

export interface LegacyMigrationPlan {
  /** Value to write under the active server's token key (null = no write). */
  token: string | null;
  /** Value to write under the active server's slug key (null = no write). */
  slug: string | null;
  /** Delete the legacy global keys after the writes above. */
  clearLegacy: boolean;
}

/**
 * Decide how to fold the legacy global session into the active server's
 * scoped keys, called once per launch from authStore.initialize().
 *
 * The legacy keys are authoritative ONLY where the scoped key is empty —
 * a scoped value always wins (it was written by a newer build after the
 * user last switched servers, so it reflects the active server's real
 * session; overwriting it with the stale global value would resurrect a
 * session the user already replaced or logged out). Both legacy keys
 * clear together once handled: they are a pair from one old build, and
 * leaving either behind turns every future launch into a migration probe.
 */
export function planLegacyMigration(
  input: LegacyMigrationInput,
): LegacyMigrationPlan {
  const { legacyToken, legacySlug, scopedToken, scopedSlug } = input;
  const hasLegacy = legacyToken !== null || legacySlug !== null;
  if (!hasLegacy) {
    return { token: null, slug: null, clearLegacy: false };
  }
  return {
    token: scopedToken === null ? legacyToken : null,
    slug: scopedSlug === null ? legacySlug : null,
    clearLegacy: true,
  };
}
