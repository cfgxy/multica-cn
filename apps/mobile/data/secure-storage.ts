/**
 * Per-server session storage on expo-secure-store: the auth token and the
 * last-used workspace slug, keyed by ServerEntry.id so switching servers
 * restores the target server's saved session instead of signing out
 * (mirrors the single-key shape of packages/core/auth/store.ts, but scoped).
 *
 * Key derivation and legacy-migration decisions live in session-keys.ts
 * (pure, unit-testable); this module only touches native storage.
 */
import * as SecureStore from "expo-secure-store";

import {
  LEGACY_SLUG_KEY,
  LEGACY_TOKEN_KEY,
  planLegacyMigration,
  slugKeyFor,
  tokenKeyFor,
} from "./session-keys";

export async function getToken(serverId: string): Promise<string | null> {
  return SecureStore.getItemAsync(tokenKeyFor(serverId));
}

export async function setToken(
  serverId: string,
  token: string,
): Promise<void> {
  await SecureStore.setItemAsync(tokenKeyFor(serverId), token);
}

export async function clearToken(serverId: string): Promise<void> {
  await SecureStore.deleteItemAsync(tokenKeyFor(serverId));
}

export async function getSlug(serverId: string): Promise<string | null> {
  return SecureStore.getItemAsync(slugKeyFor(serverId));
}

export async function setSlug(
  serverId: string,
  slug: string,
): Promise<void> {
  await SecureStore.setItemAsync(slugKeyFor(serverId), slug);
}

export async function clearSlug(serverId: string): Promise<void> {
  await SecureStore.deleteItemAsync(slugKeyFor(serverId));
}

/**
 * Purge everything a server entry owns — called from serverStore.removeServer
 * so a deleted server leaves no orphaned credentials behind.
 */
export async function clearServerSession(serverId: string): Promise<void> {
  await clearToken(serverId);
  await clearSlug(serverId);
}

/**
 * One-time upgrade: fold the pre-snapshot global token/slug into the then-
 * active server's scoped keys. Scoped values win over legacy ones (they were
 * written by a newer build and reflect the server's real session); the legacy
 * keys clear together so every later launch skips the probe. Safe to call
 * repeatedly — a no-op once the legacy keys are gone.
 */
export async function migrateLegacySession(
  serverId: string,
): Promise<void> {
  const [legacyToken, legacySlug, scopedToken, scopedSlug] =
    await Promise.all([
      SecureStore.getItemAsync(LEGACY_TOKEN_KEY),
      SecureStore.getItemAsync(LEGACY_SLUG_KEY),
      getToken(serverId),
      getSlug(serverId),
    ]);
  const plan = planLegacyMigration({
    legacyToken,
    legacySlug,
    scopedToken,
    scopedSlug,
  });
  if (!plan.clearLegacy) return;
  if (plan.token !== null) await setToken(serverId, plan.token);
  if (plan.slug !== null) await setSlug(serverId, plan.slug);
  await Promise.all([
    SecureStore.deleteItemAsync(LEGACY_TOKEN_KEY),
    SecureStore.deleteItemAsync(LEGACY_SLUG_KEY),
  ]);
}
