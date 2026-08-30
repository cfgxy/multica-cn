/**
 * Per-server session key derivation and legacy migration planning — pure
 * logic behind secure-storage.ts (which touches expo-secure-store and thus
 * can't be loaded under vitest).
 */

import { describe, expect, it } from "vitest";

import {
  LEGACY_SLUG_KEY,
  LEGACY_TOKEN_KEY,
  planLegacyMigration,
  slugKeyFor,
  tokenKeyFor,
} from "./session-keys";

describe("session key derivation", () => {
  it("scopes keys by server id", () => {
    expect(tokenKeyFor("srv_abc")).toBe("multica_token.srv_abc");
    expect(slugKeyFor("srv_abc")).toBe("multica_workspace_slug.srv_abc");
    // Different servers must never collide on the same SecureStore key.
    expect(tokenKeyFor("srv_a")).not.toBe(tokenKeyFor("srv_b"));
    expect(slugKeyFor("srv_a")).not.toBe(slugKeyFor("srv_b"));
  });

  it("never emits characters outside expo-secure-store's key charset", () => {
    // expo-secure-store validates keys against /^[\w.-]+$/ and REJECTS
    // everything else. The original separator was ":" (RUYI-31): every
    // getToken/setToken rejected, the rejection escaped initialize()'s
    // un-caught await chain, and the app hung on the boot spinner forever.
    const VALID = /^[\w.-]+$/;
    for (const serverId of ["default", "srv_abc", "srv_123", "x".repeat(64)]) {
      expect(tokenKeyFor(serverId)).toMatch(VALID);
      expect(slugKeyFor(serverId)).toMatch(VALID);
    }
  });

  it("keeps the legacy global key constants aligned with the pre-snapshot build", () => {
    // These literals are what older app versions wrote; changing them
    // orphans the migration source. Update only with a new migration leg.
    expect(LEGACY_TOKEN_KEY).toBe("multica_token");
    expect(LEGACY_SLUG_KEY).toBe("multica_current_workspace_slug");
  });
});

describe("planLegacyMigration", () => {
  it("migrates the global session to the active server when no scoped value exists", () => {
    expect(
      planLegacyMigration({
        legacyToken: "tok",
        legacySlug: "ws",
        scopedToken: null,
        scopedSlug: null,
      }),
    ).toEqual({ token: "tok", slug: "ws", clearLegacy: true });
  });

  it("keeps partial legacy values: slug migrates even when the token was never stored", () => {
    expect(
      planLegacyMigration({
        legacyToken: null,
        legacySlug: "ws",
        scopedToken: null,
        scopedSlug: null,
      }),
    ).toEqual({ token: null, slug: "ws", clearLegacy: true });
  });

  it("scoped values win — a replaced or logged-out session must not resurrect", () => {
    expect(
      planLegacyMigration({
        legacyToken: "stale",
        legacySlug: "stale-ws",
        scopedToken: "current",
        scopedSlug: null,
      }),
    ).toEqual({ token: null, slug: "stale-ws", clearLegacy: true });
  });

  it("is a no-op when the legacy keys are absent", () => {
    expect(
      planLegacyMigration({
        legacyToken: null,
        legacySlug: null,
        scopedToken: null,
        scopedSlug: null,
      }),
    ).toEqual({ token: null, slug: null, clearLegacy: false });
    // Post-migration steady state: scoped values exist, legacy keys gone.
    expect(
      planLegacyMigration({
        legacyToken: null,
        legacySlug: null,
        scopedToken: "tok",
        scopedSlug: "ws",
      }),
    ).toEqual({ token: null, slug: null, clearLegacy: false });
  });
});
