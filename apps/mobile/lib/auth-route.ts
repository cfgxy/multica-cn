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
 * The (app)/_layout gate decision. Ordering matters: startup resolution
 * wins over auth, and while it resolves the gate must HOLD rather than
 * redirect — a cold-start deep link's target route is already in the
 * stack at that point, and any navigation here discards it.
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
