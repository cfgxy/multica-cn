// Cross-provider `runtime_config.allow_subagents` override.
//
// Stored under `agent.runtime_config` alongside provider-specific schemas
// (e.g. openclaw's). Explicit true/false is an agent-level override; an absent
// key uses the provider default. Codex is the deliberate exception to the
// generic default: native subagents stay off until explicitly enabled because
// Multica cannot wait for unfinished child threads after the parent completes.
// The daemon decodes the same key in
// `server/internal/daemon/execenv/runtime_skill_policy.go` — keep both sides
// in lockstep when changing the field name.

// Whether the agent's UI toggle should be on. Explicit booleans always win;
// malformed/missing config falls back to the provider's safe default.
export function allowsSubagents(
  runtimeConfig: unknown,
  provider = "",
): boolean {
  if (runtimeConfig && typeof runtimeConfig === "object" && !Array.isArray(runtimeConfig)) {
    const explicit = (runtimeConfig as Record<string, unknown>).allow_subagents;
    if (typeof explicit === "boolean") return explicit;
  }
  return provider !== "codex";
}

// Return `runtimeConfig` with an explicit owner decision. Keeping true on the
// wire matters for Codex, whose missing-key default is intentionally off.
export function mergeSubagentAllowance(
  runtimeConfig: unknown,
  allow: boolean,
): Record<string, unknown> {
  const base =
    runtimeConfig && typeof runtimeConfig === "object" && !Array.isArray(runtimeConfig)
      ? { ...(runtimeConfig as Record<string, unknown>) }
      : {};
  base.allow_subagents = allow;
  return base;
}
