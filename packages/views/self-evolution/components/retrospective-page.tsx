"use client";

import { ModelConfigCard } from "./model-config-card";
import { RetrospectiveTab } from "./retrospective-tab";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { useWorkspaceId } from "@multica/core/hooks";

/**
 * The retrospective page (RUYI-551 §3.1, fig 8): the task settings and run
 * records, fronted by the LLM config card. Before this page existed, an
 * unconfigured LLM was a dead end — runs failed with a bare env-var error
 * and no UI to fix it; the card gives the configuration a home next to the
 * records its failures land in.
 */
export function RetrospectivePage() {
  const wsId = useWorkspaceId();
  return (
    <SelfEvolutionShell>
      <div className="flex min-w-0 flex-col gap-4 overflow-y-auto">
        <ModelConfigCard wsId={wsId} />
        <RetrospectiveTab wsId={wsId} />
      </div>
    </SelfEvolutionShell>
  );
}
