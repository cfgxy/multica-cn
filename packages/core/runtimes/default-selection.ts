import type { AgentRuntime } from "../types";
import { isRuntimeUsableForUser } from "./access";

/** Prefer the caller's Linux account associated with this workspace.
 * An offline or ambiguous association requires an explicit choice rather than
 * silently using another account's provider credentials. */
export function defaultAgentRuntime(
  runtimes: AgentRuntime[],
  userId: string | null,
): AgentRuntime | null {
  if (!userId) return null;
  const associated = runtimes.filter(
    (runtime) =>
      runtime.owner_id === userId &&
      runtime.execution_source?.preferred === true,
  );
  if (associated.length) {
    if (
      new Set(associated.map((runtime) => runtime.execution_source!.binding_id))
        .size > 1
    )
      return null;
    return (
      associated.find(
        (runtime) =>
          runtime.status === "online" &&
          isRuntimeUsableForUser(runtime, userId),
      ) ?? null
    );
  }
  return (
    runtimes.find(
      (runtime) =>
        runtime.status === "online" && isRuntimeUsableForUser(runtime, userId),
    ) ?? null
  );
}
