"use client";

import { VersionsTab } from "./versions-tab";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { useWorkspaceId } from "@multica/core/hooks";

/** The versions page (RUYI-551 §2.1): the existing tab surface under its own route. */
export function VersionsPage() {
  const wsId = useWorkspaceId();
  return (
    <SelfEvolutionShell>
      <div className="flex min-w-0 flex-col overflow-y-auto">
        <VersionsTab wsId={wsId} />
      </div>
    </SelfEvolutionShell>
  );
}
