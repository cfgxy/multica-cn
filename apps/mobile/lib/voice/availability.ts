/**
 * Voice availability gate (RUYI-694 D3, design 板5 回退形态). Pure — the
 * data layer (data/voice-providers.ts) resolves the D2 chain's inputs from
 * the workspace settings bag + the provider pool, this file decides.
 *
 * Gate semantics (dispatch card, merged D1/D2 baseline):
 *
 *   - Neither the agent-level override nor the workspace default names a
 *     provider → 回退 (unconfigured). Text sending is never affected.
 *   - The named provider is absent from the pool or disabled → 回退.
 *   - No usable credential (status not_configured / missing) → 回退.
 *   - Last connectivity probe says "invalid" (the provider explicitly
 *     rejected the key) → 回退.
 *   - "unreachable" NEVER gates the entry — a network verdict is not a
 *     credential verdict (RUYI-619 boundary); the session start may still
 *     succeed, and a dial-time failure degrades inside the session only.
 *     A probe that never ran (skipped / absent) stays available too: the
 *     gateway re-authenticates at dial time.
 *   - 连接参数修改后视为重新首次: the admin's save re-probes server-side
 *     (保存即探针) and the pool refetch re-derives this gate — no
 *     client-side caching of a verdict beyond the query lifetime.
 */

/** The resolved pool entry, narrowed to the fields the gate reads. */
export interface VoiceAvailabilityProvider {
  enabled?: boolean;
  credential_status?: string | null;
  credential_probe?: { status?: string | null } | null;
}

export interface VoiceAvailabilityInput {
  /** D2 agent-level override (`agent.voice_provider_id`). */
  agentVoiceProviderId?: string | null;
  /** `workspace.settings.voice.default.provider_id`. */
  workspaceDefaultProviderId?: string | null;
  /** The pool entry for the resolved provider id; null when absent. */
  provider: VoiceAvailabilityProvider | null;
}

export type VoiceAvailabilityReason =
  | "unconfigured"
  | "provider_missing"
  | "provider_disabled"
  | "credential_missing"
  | "credential_invalid";

export type VoiceAvailabilityOutcome =
  | { available: true }
  | { available: false; reason: VoiceAvailabilityReason };

export function resolveVoiceAvailability(
  input: VoiceAvailabilityInput,
): VoiceAvailabilityOutcome {
  const providerId =
    input.agentVoiceProviderId || input.workspaceDefaultProviderId || null;
  if (!providerId) return { available: false, reason: "unconfigured" };

  const provider = input.provider;
  if (!provider) return { available: false, reason: "provider_missing" };
  if (provider.enabled === false) {
    return { available: false, reason: "provider_disabled" };
  }

  const status = provider.credential_status;
  if (status === "not_configured" || status === "missing") {
    return { available: false, reason: "credential_missing" };
  }
  if (status === "invalid") {
    return { available: false, reason: "credential_invalid" };
  }

  const probe = provider.credential_probe?.status;
  if (probe === "invalid") {
    return { available: false, reason: "credential_invalid" };
  }

  // ok / unreachable / skipped / never-run: the entry stays. An
  // unreachable verdict only ever degrades inside a session (dial-time
  // error frame → in-session degrade copy), never here.
  return { available: true };
}
