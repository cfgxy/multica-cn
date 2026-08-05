import type { Agent } from "../types";

/**
 * Mirrors `service.MikaSystemKey` on the server. Mika is identified by this
 * key and never by display name — the name is owner-editable, so a rename
 * would otherwise make the workspace look like it has no Mika.
 */
export const MIKA_SYSTEM_KEY = "mika";

export function isMikaAgent(agent: Pick<Agent, "system_key">): boolean {
  return agent.system_key === MIKA_SYSTEM_KEY;
}

/**
 * Whether the workspace still needs a Mika provisioned.
 *
 * Deliberately "no Mika" rather than "no agents at all": the recovery
 * entrypoint on the Runtimes page used the latter, so creating any ordinary
 * agent first hid the only surface that can mint a Mika — and the generic
 * agent endpoint cannot, since it accepts neither `kind` nor `system_key`.
 */
export function workspaceNeedsMika(agents: Pick<Agent, "system_key">[]): boolean {
  return !agents.some(isMikaAgent);
}
