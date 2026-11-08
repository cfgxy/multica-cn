import type { AgentVisibility } from "../types";

/**
 * Display labels for agent visibility. The DB stores `private` as the value
 * but the UI surface name is "Personal" — it reads better next to "Workspace"
 * and matches the wording used in the access picker.
 */
export const VISIBILITY_LABEL: Record<AgentVisibility, string> = {
  workspace: "Workspace",
  private: "Personal",
};

/**
 * Descriptions for the visibility CHOICE (create dialog / picker), where
 * "Personal" is submitted as `permission_mode: "private"`. The agent owner
 * and workspace owner may invoke it; workspace admins do not bypass this gate
 * (`canInvokeAgent` in `server/internal/handler/agent_access.go`).
 */
export const VISIBILITY_DESCRIPTION: Record<AgentVisibility, string> = {
  workspace: "All members can assign",
  private: "Only you or the workspace owner can assign",
};

/**
 * Tooltip suitable for read-only badges on hover / list rows. Worded for an
 * EXISTING agent, where `visibility` is the lossy two-state projection of the
 * permission model: `private` covers both an owner-restricted agent and one
 * shared with specific people, so the copy names the applicable grants rather
 * than promising either extreme. It must not claim workspace admins can
 * assign — admins keep management + view access, not invocation.
 */
export const VISIBILITY_TOOLTIP: Record<AgentVisibility, string> = {
  workspace: "Workspace — all members can assign",
  private: "Personal — the agent owner, workspace owner, and permitted people can assign",
};

export function visibilityLabel(v: AgentVisibility): string {
  return VISIBILITY_LABEL[v];
}
