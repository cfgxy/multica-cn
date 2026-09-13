// Cross-provider `runtime_config.allow_subagents` toggle.
//
// Stored under `agent.runtime_config` alongside provider-specific schemas
// (e.g. openclaw's). Absent/false keeps the platform's default posture: the
// daemon injects `permissions.deny: ["Agent", "Task"]` into every task
// process, so the agent cannot delegate work to subagents. `true` opts the
// agent in. The daemon decodes the same key in
// `server/internal/daemon/execenv/runtime_skill_policy.go` — keep both sides
// in lockstep when changing the field name.

// Whether the agent is allowed to use subagent/task-delegation tools. Anything
// but an explicit `true` (missing key, malformed config, wrong type) reads as
// denied — fail-closed, matching the daemon.
export function allowsSubagents(runtimeConfig: unknown): boolean {
  if (!runtimeConfig || typeof runtimeConfig !== "object" || Array.isArray(runtimeConfig)) {
    return false;
  }
  return (runtimeConfig as Record<string, unknown>).allow_subagents === true;
}

// Return `runtimeConfig` with the subagent allowance applied. Denying removes
// the key entirely so saved configs stay canonical (absent = default = deny),
// and other keys — provider-specific or future — are preserved untouched.
export function mergeSubagentAllowance(
  runtimeConfig: unknown,
  allow: boolean,
): Record<string, unknown> {
  const base =
    runtimeConfig && typeof runtimeConfig === "object" && !Array.isArray(runtimeConfig)
      ? { ...(runtimeConfig as Record<string, unknown>) }
      : {};
  if (allow) {
    base.allow_subagents = true;
  } else {
    delete base.allow_subagents;
  }
  return base;
}
