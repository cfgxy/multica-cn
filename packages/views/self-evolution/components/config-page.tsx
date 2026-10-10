"use client";

import { ModelConfigCard } from "./model-config-card";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { useT } from "../../i18n";
import { useWorkspaceId } from "@multica/core/hooks";

/**
 * The module-config page (RUYI-551 §3): the single model-service card. This
 * is the closure point of the configuration domain — the workspace LLM
 * endpoint serves quality scoring alone since RUYI-552 moved the daily
 * retrospective to execution agents, so it is configured here and nowhere
 * else; Langfuse is system-level and deliberately absent (the quality page's
 * status card explains it instead of faking a workspace switch).
 */
export function SelfEvolutionConfigPage() {
  const { t } = useT("self-evolution");
  const wsId = useWorkspaceId();
  return (
    <SelfEvolutionShell>
      <div className="flex min-w-0 flex-col gap-4 overflow-y-auto">
        <div className="flex flex-col gap-1">
          <h2 className="text-title font-semibold">{t(($) => $.configPage.title)}</h2>
          <p className="text-body text-muted-foreground">{t(($) => $.configPage.description)}</p>
        </div>
        <ModelConfigCard wsId={wsId} showConsumers />
      </div>
    </SelfEvolutionShell>
  );
}
