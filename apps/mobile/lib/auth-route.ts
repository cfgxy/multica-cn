export function shouldRenderAuthenticatedStack(
  hasUser: boolean,
  isLoading: boolean,
  isServerSwitching: boolean,
  hadAuthenticatedSession: boolean,
): boolean {
  return hasUser || (hadAuthenticatedSession && (isLoading || isServerSwitching));
}

export function shouldHandleUnauthorized(
  isServerSwitching: boolean,
): boolean {
  return !isServerSwitching;
}

/**
 * Startup phase from data/startup-server-store, mirrored as a plain union
 * so lib/ stays dependency-free.
 */
export type StartupPhase = "checking" | "select" | "ready";

export type AppGateDecision =
  | { kind: "server-select" }
  | { kind: "startup-hold" }
  | { kind: "login" }
  | { kind: "stack" };

/**
 * The (app)/_layout gate decision. Ordering matters: the gate must never
 * navigate while startup is in flight — a cold-start deep link's target
 * route is already in the stack at that point, and any navigation here
 * discards it (RUYI-346 QA rounds 1-2: force-start deep links landed on
 * the default Inbox).
 *
 * Phase semantics (RUYI-346 round 3):
 * - "checking" — startup is in flight; the store auto-connects a resolvable
 *   target during this phase, so the only correct decision is to hold.
 * - "select"   — startup has CONVERGED on "the user must pick a server"
 *   (multiple servers, none resolvable as previous). This is the one
 *   navigation the startup window may emit: no deep-link content can render
 *   without a connected server.
 * - "ready"    — the startup store has converged, but auth session
 *   restoration (initialize) may still be in flight; hold until it settles
 *   so the restored session — not the login screen — claims the route.
 */
export function resolveAppGate(
  startupPhase: StartupPhase,
  hasUser: boolean,
  isLoading: boolean,
  isServerSwitching: boolean,
  hadAuthenticatedSession: boolean,
): AppGateDecision {
  if (startupPhase === "select") return { kind: "server-select" };
  if (startupPhase !== "ready") return { kind: "startup-hold" };
  if (!hasUser && isLoading && !hadAuthenticatedSession) {
    return { kind: "startup-hold" };
  }
  if (
    !shouldRenderAuthenticatedStack(
      hasUser,
      isLoading,
      isServerSwitching,
      hadAuthenticatedSession,
    )
  ) {
    return { kind: "login" };
  }
  return { kind: "stack" };
}
