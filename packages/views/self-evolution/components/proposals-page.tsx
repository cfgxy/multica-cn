"use client";

import { ProposalTab } from "./proposal-tab";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { useWorkspaceId } from "@multica/core/hooks";

/**
 * The proposals page (RUYI-551 §7.6): the pool moves under its own route
 * unchanged — RUYI-305's model replacement is still in flight, so this
 * surface is migrated, not redesigned.
 */
export function ProposalsPage() {
  const wsId = useWorkspaceId();
  return (
    <SelfEvolutionShell>
      <div className="flex min-w-0 flex-col overflow-y-auto">
        <ProposalTab wsId={wsId} />
      </div>
    </SelfEvolutionShell>
  );
}
