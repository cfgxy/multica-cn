import type { RuntimeDevice } from "../types";
import { isRuntimeUsableForUser } from "./access";

/**
 * RUYI-425 §4.2: dual-slot (text / voice) agent runtime pickers.
 *
 * An agent binds up to two instances: the classic text runtime plus an
 * optional realtime-voice instance. Both slots draw from the same runtime
 * list, so the pickers must (a) filter by the instance's advertised
 * capabilities and (b) exclude the other slot's selection — one instance can
 * never hold both slots. These pure helpers are the single source for both
 * rules so the mobile screens and any future web surface cannot drift.
 */

export type AgentSlotCapability = "text" | "realtime_voice";

/**
 * Whether the instance advertises `capability`.
 *
 * The capabilities block is Stage-2-era: older backends omit it, and an
 * instance that simply hasn't said anything must stay pickable — absence is
 * "unfilterable", not "unsupported". Only an explicit `false` removes an
 * instance from a slot.
 */
export function runtimeSupportsCapability(
  runtime: RuntimeDevice,
  capability: AgentSlotCapability,
): boolean {
  return runtime.capabilities?.[capability] !== false;
}

export type RuntimeCredentialStatus =
  | "not_configured"
  | "configured"
  | "invalid";

/**
 * Credential badge state for the voice preview row (§4.2). Older backends
 * omit the field; the badge then reads 未配置, matching a server that has no
 * credential stored.
 */
export function runtimeCredentialStatus(
  runtime: RuntimeDevice,
): RuntimeCredentialStatus {
  return runtime.credential_status ?? "not_configured";
}

export interface AgentSlotChoiceOptions {
  currentUserId: string | null;
  /**
   * The other slot's selection; excluded so one instance can't hold both
   * slots. Empty string / undefined = other slot unbound.
   */
  excludeRuntimeId?: string;
  /**
   * This slot's own current binding (edit screens). Never filtered away, so
   * an existing binding stays visible even offline — and even in the legacy
   * same-instance-on-both-slots case, where dropping it would make the form
   * lie about the stored state.
   */
  keepRuntimeId?: string;
}

/**
 * Choices for one slot of the agent runtime picker: online + usable by the
 * current user (`isRuntimeUsableForUser`, the MUL-6126 contract) + capable of
 * the slot, minus the other slot's selection.
 */
export function agentSlotChoices(
  runtimes: RuntimeDevice[] | undefined,
  capability: AgentSlotCapability,
  opts: AgentSlotChoiceOptions,
): RuntimeDevice[] {
  const usable = (runtimes ?? []).filter(
    (r) =>
      r.status === "online" &&
      isRuntimeUsableForUser(r, opts.currentUserId) &&
      runtimeSupportsCapability(r, capability),
  );
  const { excludeRuntimeId, keepRuntimeId } = opts;
  if (!excludeRuntimeId || excludeRuntimeId === keepRuntimeId) return usable;
  return usable.filter((r) => r.id !== excludeRuntimeId);
}
