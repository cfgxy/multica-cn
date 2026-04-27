// Pure derivation of an agent's user-facing "presence" state from raw
// server data. The back-end never computes this — it only stores facts
// (which tasks exist, when the runtime last heartbeated). The UI translation
// (colour, copy, time-window behaviour) is a front-end concern that may
// iterate independently of the schema.
//
// Inputs are passed in (no globals, no `Date.now()` baked in) so tests can
// exercise time-window edges and so multiple components reading the same
// agent are guaranteed to derive the same value.

import type { Agent, AgentRuntime, AgentTask, TaskFailureReason } from "../types";
import type { AgentPresence, AgentPresenceDetail } from "./types";

// How long after a failed task we keep showing the "Failed" red state.
// Short enough that the indicator clears before users wonder why it's still
// there; long enough that they actually notice it. Hard-coded for now;
// promote to config once design has confirmed copy/animation.
export const FAILED_WINDOW_MS = 2 * 60 * 1000;

interface DerivePresenceInput {
  agent: Agent;
  runtime: AgentRuntime;
  recentTasks: AgentTask[];
  now: number;
}

export function deriveAgentPresence(input: DerivePresenceInput): AgentPresence {
  // 1. Runtime offline trumps everything — without a runtime the agent
  //    physically cannot run, regardless of pending work or stale tasks.
  if (input.runtime.status === "offline") return "offline";

  // 2. A task failure within the recent window — surface red so the user
  //    notices. Older failures are ignored (they fall out of the window).
  if (findRecentFailed(input.recentTasks, input.now)) return "failed";

  // 3. Anything currently running — the agent is busy.
  if (input.recentTasks.some((t) => t.status === "running")) return "working";

  // 4. Anything queued/dispatched but not yet running — the agent has work
  //    waiting (typical when at max_concurrent_tasks or awaiting dispatch).
  if (input.recentTasks.some((t) => t.status === "queued" || t.status === "dispatched")) {
    return "pending";
  }

  // 5. Otherwise the agent is online and idle.
  return "available";
}

export function deriveAgentPresenceDetail(input: DerivePresenceInput): AgentPresenceDetail {
  const presence = deriveAgentPresence(input);
  let runningCount = 0;
  let queuedCount = 0;
  for (const t of input.recentTasks) {
    if (t.agent_id !== input.agent.id) continue;
    if (t.status === "running") runningCount += 1;
    else if (t.status === "queued" || t.status === "dispatched") queuedCount += 1;
  }

  let failureReason: TaskFailureReason | undefined;
  if (presence === "failed") {
    const recent = findRecentFailed(input.recentTasks, input.now);
    // Truthy check implicitly filters out the empty-string variant (which
    // means the back-end didn't classify the failure) — leaving only real
    // TaskFailureReason values for the UI to map to copy.
    if (recent && recent.failure_reason) {
      failureReason = recent.failure_reason;
    }
  }

  return { presence, runningCount, queuedCount, failureReason };
}

function findRecentFailed(tasks: AgentTask[], now: number): AgentTask | undefined {
  return tasks.find((t) => {
    if (t.status !== "failed") return false;
    if (!t.completed_at) return false;
    const completedAt = new Date(t.completed_at).getTime();
    if (Number.isNaN(completedAt)) return false;
    return now - completedAt < FAILED_WINDOW_MS;
  });
}
