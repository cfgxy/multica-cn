/**
 * Pure core of the mobile workspace-prefix resolution (RUYI-705 rework).
 * Dependency-free on purpose — no React Query, no store, no react-native —
 * so it stays unit-testable under vitest alongside the other pure decision
 * helpers in lib/ (link-route, soft-breaks).
 *
 * Resolution discipline — slug first, id second:
 *
 *   - The route slug is the identity the whole API layer already trusts: it
 *     IS the `X-Workspace-Slug` header `ApiClient.fetch` injects on every
 *     request, and `restoreSlug()` has it available from SecureStore before
 *     anything renders.
 *   - The id mirror is in-memory only, written by the `[workspace]/_layout`
 *     sync effect once it matched the list. On cold start (and any state
 *     where that effect has not confirmed yet) the store docs guarantee the
 *     id stays null while the slug is already usable.
 *
 * The first cut of the RUYI-705 gate matched by id alone, so exactly those
 * states fell through to "unknown prefix" and the extraction gate opened:
 * bare `AC-x` numbering came back as fake tail rows, each firing a 404
 * resolution request — QA's 2026-10-11 FAIL report reproduced it on 4
 * independent device sessions. Web never had this hole because its
 * `useCurrentWorkspace` resolves by slug (packages/core/paths/hooks.tsx);
 * this resolver gives mobile the same discipline with an id fallback for
 * states where only the id mirror is populated.
 *
 * An unknown prefix (`null`) still falls through by design: the resolver's
 * exact match stays the backstop, same as web. Callers that spend network
 * per collected token (the tail list) additionally gate on
 * `identifierMatchesPrefix` so a known-mismatched identifier never fires.
 */
import type { Workspace } from "@multica/core/types";

export function resolveWorkspacePrefix(
  workspaces: Workspace[] | undefined,
  slug: string | null,
  id: string | null,
): string | null {
  if (!workspaces) return null;
  const current =
    (slug ? workspaces.find((w) => w.slug === slug) : undefined) ??
    (id ? workspaces.find((w) => w.id === id) : undefined);
  return current?.issue_prefix || null;
}
