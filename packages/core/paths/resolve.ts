import type { Workspace } from "../types";
import { useOnboardingStore } from "../onboarding/store";
import { paths } from "./paths";

/**
 * Single source of truth for "where should an authenticated user land?"
 * Centralizes the post-auth decision used at every fork (login callback,
 * landing-page redirect, dashboard guard, workspace-deleted relocations,
 * desktop zero-ws effect).
 *
 * Priority (dev-phase + shipping semantics both):
 *   !hasOnboarded                         → /onboarding
 *   hasOnboarded && has workspace         → /<first.slug>/issues
 *   hasOnboarded && zero workspaces       → /workspaces/new
 *
 * Onboarding wins regardless of workspace state. During frontend
 * development `useHasOnboarded()` always returns `false` (in-memory
 * store resets on every page load), so every login re-enters the flow
 * — intentional, lets us iterate on each step trivially. Once the
 * backend ships, `useHasOnboarded()` reflects real `onboarded_at`,
 * and this function's output silently starts matching production
 * expectations without further changes.
 */
export function resolvePostAuthDestination(
  workspaces: Workspace[],
  hasOnboarded: boolean,
): string {
  if (!hasOnboarded) {
    return paths.onboarding();
  }
  const first = workspaces[0];
  return first ? paths.workspace(first.slug).issues() : paths.newWorkspace();
}

/**
 * Whether the current user has completed onboarding. Reads from the
 * onboarding store, which today is dev-only in-memory state (resets on
 * refresh) and later becomes backed by GET /api/me/onboarding.
 */
export function useHasOnboarded(): boolean {
  return useOnboardingStore((s) => s.state.onboarded_at !== null);
}
