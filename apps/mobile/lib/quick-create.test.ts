// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { Agent, IssuePriority, Squad } from "@multica/core/types";
import {
  buildQuickCreateBody,
  resolveQuickCreateActor,
  visibleQuickCreateActors,
} from "@/lib/quick-create";

/**
 * Mirrors the web quick-create panel's actor-visibility / seed-chain /
 * payload rules (packages/views/modals/quick-create-issue.tsx):
 *   - visibleAgents = !archived && isAgentRuntimeBound && canAssignAgent
 *   - visibleSquads = !archived && leader ∈ visibleAgents
 *   - seed chain: draft → last-actor → first visible agent
 *   - payload: agent_id|squad_id + prompt + optional project/priority/due
 * Mobile skips web's data-seeds (agent_id / squad_id modal carries) and the
 * parent-issue / attachment channels — not part of the mobile v1 flow.
 */

let seq = 0;

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  seq += 1;
  return {
    id: `agent-${seq}`,
    workspace_id: "ws-1",
    runtime_id: "rt-1",
    runtime_bound: true,
    name: `Agent ${seq}`,
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    archived_at: null,
    owner_id: "user-1",
    permission_mode: "public_to",
    invocation_targets: [{ target_type: "workspace", target_id: "ws-1" }],
    ...overrides,
  } as Agent;
}

function makeSquad(overrides: Partial<Squad> = {}): Squad {
  seq += 1;
  return {
    id: `squad-${seq}`,
    workspace_id: "ws-1",
    name: `Squad ${seq}`,
    leader_id: "agent-1",
    archived_at: null,
    ...overrides,
  } as Squad;
}

describe("visibleQuickCreateActors", () => {
  const userId = "user-1";

  it("keeps runtime-bound, non-archived, assignable agents", () => {
    const { agents } = visibleQuickCreateActors(
      [makeAgent()],
      [],
      { userId, memberRole: "member" },
    );
    expect(agents).toHaveLength(1);
  });

  it("drops archived agents", () => {
    const archived = makeAgent({ archived_at: "2026-01-01T00:00:00Z" });
    const { agents } = visibleQuickCreateActors(
      [archived, makeAgent()],
      [],
      { userId, memberRole: "member" },
    );
    expect(agents).toHaveLength(1);
  });

  it("drops agents whose runtime is unbound (runtime_bound=false)", () => {
    const unbound = makeAgent({ runtime_bound: false });
    const { agents } = visibleQuickCreateActors(
      [unbound],
      [],
      { userId, memberRole: "member" },
    );
    expect(agents).toHaveLength(0);
  });

  it("drops agents with an empty runtime_id (legacy unbound signal)", () => {
    const unbound = makeAgent({ runtime_id: "", runtime_bound: undefined });
    const { agents } = visibleQuickCreateActors(
      [unbound],
      [],
      { userId, memberRole: "member" },
    );
    expect(agents).toHaveLength(0);
  });

  it("keeps a private agent for its owner and drops it for others", () => {
    const mine = makeAgent({
      permission_mode: "private",
      invocation_targets: [],
      owner_id: userId,
    });
    const theirs = makeAgent({
      permission_mode: "private",
      invocation_targets: [],
      owner_id: "someone-else",
    });
    const { agents } = visibleQuickCreateActors(
      [mine, theirs],
      [],
      { userId, memberRole: "member" },
    );
    expect(agents.map((a) => a.id)).toEqual([mine.id]);
  });

  it("treats an unknown member role as non-member for workspace grants", () => {
    // A workspace-target grant requires a recognized membership role; web's
    // canAssignAgent wrapper normalizes anything else to null (fail closed).
    // The agent is someone else's so the owner bypass can't mask the role
    // check.
    const someoneElses = makeAgent({ owner_id: "someone-else" });
    const { agents } = visibleQuickCreateActors(
      [someoneElses],
      [],
      { userId, memberRole: undefined },
    );
    expect(agents).toHaveLength(0);
  });

  it("drops archived squads and squads whose leader is not visible", () => {
    const leader = makeAgent();
    const archivedSquad = makeSquad({ archived_at: "2026-01-01T00:00:00Z" });
    const orphanSquad = makeSquad({ leader_id: "missing-leader" });
    const okSquad = makeSquad({ leader_id: leader.id });
    const { squads } = visibleQuickCreateActors(
      [leader],
      [archivedSquad, orphanSquad, okSquad],
      { userId, memberRole: "member" },
    );
    expect(squads.map((s) => s.id)).toEqual([okSquad.id]);
  });
});

describe("resolveQuickCreateActor", () => {
  const visible = visibleQuickCreateActors(
    [makeAgent(), makeAgent()],
    [makeSquad({ leader_id: "agent-1" })],
    { userId: "user-1", memberRole: "member" },
  );

  it("returns the first candidate that is still visible", () => {
    const second = visible.agents[1];
    const resolved = resolveQuickCreateActor(
      [{ type: "squad", id: "nope" }, { type: "agent", id: second.id }],
      visible.agents,
      visible.squads,
      true,
    );
    expect(resolved).toEqual({ type: "agent", id: second.id });
  });

  it("skips candidates that vanished (archived / revoked) and keeps walking", () => {
    const first = visible.agents[0];
    const resolved = resolveQuickCreateActor(
      [
        { type: "agent", id: "deleted-agent" },
        { type: "agent", id: first.id },
      ],
      visible.agents,
      visible.squads,
      true,
    );
    expect(resolved).toEqual({ type: "agent", id: first.id });
  });

  it("accepts a visible squad candidate", () => {
    const squad = visible.squads[0];
    const resolved = resolveQuickCreateActor(
      [{ type: "squad", id: squad.id }],
      visible.agents,
      visible.squads,
      true,
    );
    expect(resolved).toEqual({ type: "squad", id: squad.id });
  });

  it("falls back to the first visible agent when no candidate matches", () => {
    const resolved = resolveQuickCreateActor(
      [{ type: "agent", id: "gone" }],
      visible.agents,
      visible.squads,
      true,
    );
    expect(resolved).toEqual({ type: "agent", id: visible.agents[0].id });
  });

  it("returns null when nothing is visible", () => {
    const resolved = resolveQuickCreateActor([], [], [], true);
    expect(resolved).toBeNull();
  });

  // RUYI-130: the squad list defaults to `[]` while its query is in flight.
  // Resolving against that empty set made a remembered squad look deleted
  // and the chain fell through to the first visible agent — which in the
  // reported workspace IS the squad's leader, so the pick silently became
  // "the leader" instead of "the squad".
  it("does not resolve at all until both actor lists have loaded", () => {
    const squad = visible.squads[0];
    const resolved = resolveQuickCreateActor(
      [{ type: "squad", id: squad.id }],
      visible.agents,
      [],
      false,
    );
    expect(resolved).toBeNull();
  });

  it("keeps a remembered squad once the squad list resolves", () => {
    const squad = visible.squads[0];
    const resolved = resolveQuickCreateActor(
      [null, { type: "squad", id: squad.id }],
      visible.agents,
      visible.squads,
      true,
    );
    expect(resolved).toEqual({ type: "squad", id: squad.id });
  });

  it("never downgrades a remembered squad to its leader agent", () => {
    // This fixture's squad leader IS the first visible agent, i.e. the
    // seed-chain tail — exactly the RUYI-130 shape. A fall-through would
    // therefore be indistinguishable from a successful restore in the UI.
    const squad = visible.squads[0];
    expect(squad.leader_id).toBe(visible.agents[0].id);
    const resolved = resolveQuickCreateActor(
      [{ type: "squad", id: squad.id }],
      visible.agents,
      visible.squads,
      true,
    );
    expect(resolved).toEqual({ type: "squad", id: squad.id });
  });

  // RUYI-130 rework: an AsyncStorage read failure leaves the last-actor
  // memory empty, which is indistinguishable from "nothing was ever filed
  // here". Falling through to the tail in that state re-creates the reported
  // downgrade, so the tail needs its own gate.
  it("withholds the first-visible-agent tail while the last-actor history is unresolved", () => {
    const resolved = resolveQuickCreateActor(
      [],
      visible.agents,
      visible.squads,
      true,
      false,
    );
    expect(resolved).toBeNull();
  });

  it("still resolves an explicit pick while the history is unresolved", () => {
    // An unreadable memory must not lock the flow: what the user just picked
    // needs no history to be trustworthy.
    const squad = visible.squads[0];
    const resolved = resolveQuickCreateActor(
      [{ type: "squad", id: squad.id }],
      visible.agents,
      visible.squads,
      true,
      false,
    );
    expect(resolved).toEqual({ type: "squad", id: squad.id });
  });
});

describe("buildQuickCreateBody", () => {
  it("maps an agent pick to agent_id and omits every unset optional field", () => {
    const body = buildQuickCreateBody({
      actor: { type: "agent", id: "a-1" },
      prompt: "Fix the inbox loading slowness",
      projectId: null,
      priority: "none",
      dueDate: null,
    });
    expect(body).toEqual({
      agent_id: "a-1",
      prompt: "Fix the inbox loading slowness",
    });
  });

  it("maps a squad pick to squad_id", () => {
    const body = buildQuickCreateBody({
      actor: { type: "squad", id: "s-1" },
      prompt: "File the release checklist",
      projectId: null,
      priority: "none",
      dueDate: null,
    });
    expect(body).toEqual({ squad_id: "s-1", prompt: "File the release checklist" });
  });

  it("carries explicit priority / due date / project and drops 'none'/null", () => {
    const body = buildQuickCreateBody({
      actor: { type: "agent", id: "a-1" },
      prompt: "p",
      projectId: "proj-1",
      priority: "high" as IssuePriority,
      dueDate: "2026-09-10",
    });
    expect(body).toEqual({
      agent_id: "a-1",
      prompt: "p",
      project_id: "proj-1",
      priority: "high",
      due_date: "2026-09-10",
    });
  });
});
