// ---------------------------------------------------------------------------
// Execution profiles (RUYI-57)
//
// A workspace-level named set of per-agent execution configuration. Activating
// one overwrites runtime / model / thinking level on exactly the agents it
// names, so a whole squad can be moved between providers in one click when a
// token supply goes bad.
//
// Naming: the UI calls this "Profile", and so does RuntimeProfile (MUL-3284),
// which is a different thing entirely — how a custom runtime is launched.
// Everything in code says ExecutionProfile so the two never get confused.
// ---------------------------------------------------------------------------

// One member's configuration inside a profile.
//
// `runtime_id` and `model` are always both present: the server refuses to
// store a half-filled entry, because a stored entry must be activatable.
//
// `thinking_level` is tri-state, the same shape the single-agent API uses:
//   null — the profile has no opinion; activation leaves the agent's level
//          alone.
//   ""   — the profile says "runtime default"; activation CLEARS the agent's
//          level. Without this state an activation would overwrite runtime and
//          model while a stale level survived next to them.
//   value — written as-is.
export interface ExecutionProfileEntry {
  agent_id: string;
  runtime_id: string;
  model: string;
  thinking_level: string | null;
  updated_at: string;
}

export interface ExecutionProfile {
  id: string;
  workspace_id: string;
  name: string;
  description: string | null;
  created_by: string | null;
  // Which profile the workspace pointer names. Display state only: it says
  // which profile last wrote the agents, not that the agents still match it —
  // editing one agent afterwards drifts from the profile without clearing it.
  is_active: boolean;
  entry_count: number;
  last_activated_at: string | null;
  created_at: string;
  updated_at: string;
  // Populated by the detail read; the list read returns an empty array and
  // reports size through `entry_count` instead.
  entries: ExecutionProfileEntry[];
}

export interface ExecutionProfileListResponse {
  execution_profiles: ExecutionProfile[];
  active_execution_profile_id: string | null;
}

export interface CreateExecutionProfileRequest {
  name: string;
  description?: string;
}

export interface UpdateExecutionProfileRequest {
  name?: string;
  description?: string;
}

// PUT body for one member's slot. `thinking_level` carries the same tri-state
// as the stored entry: omitted / null = no opinion, "" = clear to the runtime
// default on activation, value = write it.
export interface UpsertExecutionProfileEntryRequest {
  agent_id: string;
  runtime_id: string;
  model: string;
  thinking_level?: string | null;
}

// What happened to one member during activation.
// - applied: the agent was reconfigured.
// - skipped: the entry no longer names a live agent (archived, or removed
//   from the workspace since the entry was saved).
// - failed: the entry is still valid but could not be written — an offline or
//   deleted runtime, or a thinking level its provider no longer accepts.
export type ExecutionProfileActivationStatus =
  | "applied"
  | "skipped"
  | "failed";

export interface ExecutionProfileActivationResult {
  agent_id: string;
  status: ExecutionProfileActivationStatus;
  // Machine-readable; the UI maps it to a localized sentence. Present for
  // every non-applied result.
  reason?: string;
}

// The activation response always enumerates every entry, so the result dialog
// can name the members that did not move instead of showing only a count.
//
// `applied === 0` is the all-failed case: the server rolled the whole
// transaction back, so nothing was written and `profile.is_active` is false.
export interface ExecutionProfileActivationResponse {
  profile: ExecutionProfile;
  applied: number;
  skipped: number;
  failed: number;
  results: ExecutionProfileActivationResult[];
}
