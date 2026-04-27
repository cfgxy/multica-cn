// Derived "presence" types for agents — the user-facing state we display
// across the UI (list dots, hover cards, badges). Computed in the front-end
// from raw server data (agent + runtime + recent tasks); the back-end never
// knows about these enums.

import type { TaskFailureReason } from "../types";

export type AgentPresence =
  | "available" // 🟢 runtime online, no active tasks
  | "working" // 🔵 runtime online, at least one running task
  | "pending" // 🟡 runtime online, no running but ≥1 queued/dispatched
  | "failed" // 🔴 a task failed within the recent-failed window (default 2 min)
  | "offline"; // ⚫ runtime offline (covers daemon down, CLI missing, etc.)

export interface AgentPresenceDetail {
  presence: AgentPresence;
  runningCount: number;
  queuedCount: number;
  // Set only when presence === "failed". The label lookup happens at the UI
  // layer; deriving exposes the raw classifier so the UI can choose copy.
  failureReason?: TaskFailureReason;
}
