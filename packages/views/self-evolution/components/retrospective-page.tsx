"use client";

import { RetrospectiveTab } from "./retrospective-tab";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { useWorkspaceId } from "@multica/core/hooks";

/**
 * The retrospective page (RUYI-551 §3.1, fig 8): the task settings and run
 * records of the agent-driven daily retrospective (RUYI-552). No model-service
 * card here — the run consumes the execution agent, not the workspace LLM
 * endpoint, so an unconfigured run is guided by the tab's agent selector
 * instead; the endpoint serves quality scoring and lives on the module-config
 * page (RUYI-658).
 */
export function RetrospectivePage() {
  const wsId = useWorkspaceId();
  return (
    <SelfEvolutionShell>
      <div className="flex min-w-0 flex-col gap-4 overflow-y-auto">
        <RetrospectiveTab wsId={wsId} />
      </div>
    </SelfEvolutionShell>
  );
}
