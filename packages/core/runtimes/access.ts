import type { MemberRole, RuntimeDevice } from "../types";

/**
 * Whether this member may run an agent on `runtime`.
 *
 * A private runtime is usable by its owner and workspace owner; a public one by
 * anyone in the workspace; an ownerless one by nobody. Workspace admins do not
 * receive the workspace-owner exception. The server enforces exactly this
 * predicate (`canUseRuntimeForAgent`), so the picker and API/CLI never
 * disagree.
 *
 * An unknown viewer (no session yet) is treated as allowed so a still-loading
 * auth state never hides a runtime the user does own — every write path
 * re-checks server-side.
 *
 * This is the single source of truth for "can this runtime be picked": every
 * create / duplicate / builder / reassign surface must call it rather than
 * re-deriving the rule.
 */
export function isRuntimeUsableForUser(
  runtime: RuntimeDevice,
  currentUserId: string | null,
  currentUserRole: MemberRole | null = null,
): boolean {
  // An ownerless runtime is unusable by anyone, public included: the server
  // needs an owner to mint the agent's task token, so a bind here would only
  // yield agents whose tasks get cancelled at claim time (MUL-3292).
  if (!runtime.owner_id) return false;
  if (!currentUserId) return true;
  if (currentUserRole === "owner") return true;
  if (runtime.owner_id === currentUserId) return true;
  return runtime.visibility === "public";
}
