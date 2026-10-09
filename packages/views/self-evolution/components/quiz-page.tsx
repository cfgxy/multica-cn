"use client";

import { QuizTab } from "./quiz-tab";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { useWorkspaceId } from "@multica/core/hooks";

/** The quiz page (RUYI-551 §2.1): the existing tab surface under its own route. */
export function QuizPage() {
  const wsId = useWorkspaceId();
  return (
    <SelfEvolutionShell>
      <div className="flex min-w-0 flex-col overflow-y-auto">
        <QuizTab wsId={wsId} />
      </div>
    </SelfEvolutionShell>
  );
}
