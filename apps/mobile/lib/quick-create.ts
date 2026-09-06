/**
 * Pure logic for the smart-mode (agent quick-create) issue flow. Design
 * mirrors web's AgentCreatePanel (`packages/views/modals/quick-create-issue.tsx`)
 * — mobile renders its own UI but must not re-derive the product rules:
 *
 *   - Visibility: an agent is pickable iff not archived, runtime-bound, and
 *     invocation-allowed for the current user (canAssignAgentToIssue, the
 *     same Decision API chat uses). A squad is pickable iff not archived and
 *     its leader agent is pickable — the backend routes a squad pick to the
 *     leader, so hiding squads with invisible leaders keeps the list honest
 *     with what the server would accept.
 *   - Seed chain: draft pick → last successful pick → first visible agent.
 *     Candidates that no longer resolve (archived, revoked, deleted) fall
 *     through instead of blocking the flow.
 *   - Payload: exactly one of agent_id / squad_id, the prompt, and the
 *     optional shared fields — `none` priority / null due date / null
 *     project are omitted so the server applies its defaults.
 *
 * Mobile v1 skips web's data-seed channel (`data.agent_id` / `squad_id`
 * modal carries) and the parent-issue / attachment payload fields — the
 * mobile entry surface has no modal registry and no prompt attachments.
 */
import type { Agent, IssuePriority, MemberRole, Squad } from "@multica/core/types";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { isAgentRuntimeBound } from "@/lib/is-agent-runtime-bound";

export type QuickCreateActorType = "agent" | "squad";

export interface QuickCreateActorRef {
  type: QuickCreateActorType;
  id: string;
}

export interface VisibleQuickCreateActors {
  agents: Agent[];
  squads: Squad[];
}

/**
 * Web's `canAssignAgent` wrapper (packages/views/issues/components/pickers/
 * assignee-picker.tsx) normalizes anything outside the three membership
 * roles to null so the Decision API fails closed on unknown values — mirror
 * that here rather than trusting the caller's shape.
 */
function normalizeRole(role: MemberRole | null | undefined): MemberRole | null {
  return role === "owner" || role === "admin" || role === "member"
    ? role
    : null;
}

export function visibleQuickCreateActors(
  agents: Agent[],
  squads: Squad[],
  ctx: { userId: string | null | undefined; memberRole: MemberRole | null | undefined },
): VisibleQuickCreateActors {
  const role = normalizeRole(ctx.memberRole);
  const visibleAgents = agents.filter(
    (a) =>
      !a.archived_at &&
      isAgentRuntimeBound(a) &&
      canAssignAgentToIssue(a, { userId: ctx.userId ?? null, role }).allowed,
  );
  const visibleAgentIds = new Set(visibleAgents.map((a) => a.id));
  const visibleSquads = squads.filter(
    (s) => !s.archived_at && visibleAgentIds.has(s.leader_id),
  );
  return { agents: visibleAgents, squads: visibleSquads };
}

/**
 * Resolve the first candidate that still exists in the visible set (web
 * `resolveActor` chain), else default to the first visible agent (web
 * `seedActor` tail), else null. `candidates` is ordered most-authoritative
 * first: [draft pick, last successful pick].
 */
export function resolveQuickCreateActor(
  candidates: (QuickCreateActorRef | null | undefined)[],
  agents: Agent[],
  squads: Squad[],
): QuickCreateActorRef | null {
  for (const candidate of candidates) {
    if (!candidate) continue;
    if (candidate.type === "squad") {
      if (squads.some((s) => s.id === candidate.id)) return candidate;
    } else if (agents.some((a) => a.id === candidate.id)) {
      return candidate;
    }
  }
  return agents[0] ? { type: "agent", id: agents[0].id } : null;
}

export interface QuickCreateBodyInput {
  actor: QuickCreateActorRef;
  prompt: string;
  projectId: string | null;
  priority: IssuePriority;
  dueDate: string | null;
}

/**
 * Request body for POST /api/issues/quick-create, field-for-field the shape
 * web's onSubmit builds (spread-conditional priority/due_date included as
 * explicit omission rules here). Callers enforce the non-empty prompt —
 * same as web's Create-button disable plus submit-time re-guard.
 */
export function buildQuickCreateBody({
  actor,
  prompt,
  projectId,
  priority,
  dueDate,
}: QuickCreateBodyInput): {
  agent_id?: string;
  squad_id?: string;
  prompt: string;
  project_id?: string;
  priority?: IssuePriority;
  due_date?: string;
} {
  return {
    ...(actor.type === "agent" ? { agent_id: actor.id } : { squad_id: actor.id }),
    prompt,
    ...(projectId !== null ? { project_id: projectId } : {}),
    ...(priority !== "none" ? { priority } : {}),
    ...(dueDate !== null ? { due_date: dueDate } : {}),
  };
}
