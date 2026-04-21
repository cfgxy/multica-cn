"use client";

import { useCallback, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { setCurrentWorkspace } from "@multica/core/platform";
import { useAuthStore } from "@multica/core/auth";
import {
  advanceOnboarding,
  completeOnboarding,
  ONBOARDING_STEP_ORDER,
  type OnboardingStep,
  type QuestionnaireAnswers,
} from "@multica/core/onboarding";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import type { Agent, AgentRuntime, Workspace } from "@multica/core/types";
import { StepHeader } from "./components/step-header";
import { StepWelcome } from "./steps/step-welcome";
import { StepQuestionnaire } from "./steps/step-questionnaire";
import { StepWorkspace } from "./steps/step-workspace";
import { StepRuntimeConnect } from "./steps/step-runtime-connect";
import { StepPlatformFork } from "./steps/step-platform-fork";
import { StepAgent } from "./steps/step-agent";
import { StepFirstIssue } from "./steps/step-first-issue";

const EMPTY_QUESTIONNAIRE: QuestionnaireAnswers = {
  team_size: null,
  team_size_other: null,
  role: null,
  role_other: null,
  use_case: null,
  use_case_other: null,
};

function mergeQuestionnaire(
  raw: Record<string, unknown>,
): QuestionnaireAnswers {
  return { ...EMPTY_QUESTIONNAIRE, ...(raw as Partial<QuestionnaireAnswers>) };
}

/**
 * Shell's onComplete contract:
 *   onComplete(workspace?, firstIssueId?) — if both are supplied,
 *   navigate to the issue detail; if only workspace, the issues list;
 *   if neither, fall back to root.
 */
export function OnboardingFlow({
  onComplete,
  runtimeInstructions,
}: {
  onComplete: (workspace?: Workspace, firstIssueId?: string) => void;
  runtimeInstructions?: React.ReactNode;
}) {
  const user = useAuthStore((s) => s.user);
  if (!user) {
    throw new Error("OnboardingFlow requires an authenticated user");
  }

  // Questionnaire answers are server-persisted and pre-fill Step 1
  // on re-entry. That's the only piece of server state the UI reads
  // directly — `current_step` is PATCHed for analytics but never
  // drives navigation; every entry starts at Welcome.
  const storedQuestionnaire = mergeQuestionnaire(user.onboarding_questionnaire);

  const [step, setStep] = useState<OnboardingStep>("welcome");
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [runtime, setRuntime] = useState<AgentRuntime | null>(null);
  const [agent, setAgent] = useState<Agent | null>(null);

  // Only needed at Step 2 to detect a pre-existing workspace from an
  // earlier abandoned onboarding — so StepWorkspace shows "Continue
  // with {name}" instead of CreateWorkspaceForm (which would hit a
  // slug conflict on submit).
  const { data: workspaces = [] } = useQuery({
    ...workspaceListOptions(),
    enabled: step === "workspace",
  });
  const existingWorkspace = workspace ?? workspaces[0] ?? null;

  const handleWelcomeNext = useCallback(async () => {
    await advanceOnboarding({ current_step: "questionnaire" });
    setStep("questionnaire");
  }, []);

  const handleQuestionnaireSubmit = useCallback(
    async (answers: QuestionnaireAnswers) => {
      await advanceOnboarding({
        current_step: "workspace",
        questionnaire: answers,
      });
      setStep("workspace");
    },
    [],
  );

  const handleWorkspaceCreated = useCallback(async (ws: Workspace) => {
    setWorkspace(ws);
    setCurrentWorkspace(ws.slug, ws.id);
    await advanceOnboarding({ current_step: "runtime" });
    setStep("runtime");
  }, []);

  const handleRuntimeNext = useCallback(async (rt: AgentRuntime | null) => {
    setRuntime(rt);
    // No runtime → no agent possible; skip Step 4 and let Step 5
    // bootstrap run the self-serve path with agent=null.
    const next: OnboardingStep = rt ? "agent" : "first_issue";
    await advanceOnboarding({ current_step: next });
    setStep(next);
  }, []);

  const handleAgentCreated = useCallback(async (created: Agent) => {
    setAgent(created);
    await advanceOnboarding({ current_step: "first_issue" });
    setStep("first_issue");
  }, []);

  const handleAgentSkip = useCallback(async () => {
    await advanceOnboarding({ current_step: "first_issue" });
    setStep("first_issue");
  }, []);

  const handleBack = useCallback((from: OnboardingStep) => {
    const idx = ONBOARDING_STEP_ORDER.indexOf(from);
    if (idx <= 0) return;
    const prev = ONBOARDING_STEP_ORDER[idx - 1]!;
    setStep(prev);
  }, []);

  // complete() is idempotent server-side, so a failed call surfaces
  // a toast and stays on the current step. Bubbling to the error
  // boundary would trap the user with no retry path.
  const handleBootstrapDone = useCallback(
    async (firstIssueId: string | null) => {
      try {
        await completeOnboarding();
      } catch (err) {
        toast.error(
          err instanceof Error ? err.message : "Failed to finish onboarding",
        );
        return;
      }
      onComplete(workspace ?? undefined, firstIssueId ?? undefined);
    },
    [workspace, onComplete],
  );

  const handleBootstrapSkip = useCallback(async () => {
    try {
      await completeOnboarding();
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to finish onboarding",
      );
      return;
    }
    onComplete(workspace ?? undefined);
  }, [workspace, onComplete]);

  if (step === "welcome") {
    return <StepWelcome onNext={handleWelcomeNext} />;
  }

  return (
    <div className="flex w-full flex-col gap-8">
      <StepHeader currentStep={step} />
      {step === "questionnaire" && (
        <StepQuestionnaire
          initial={storedQuestionnaire}
          onSubmit={handleQuestionnaireSubmit}
        />
      )}
      {step === "workspace" && (
        <StepWorkspace
          existing={existingWorkspace}
          onCreated={handleWorkspaceCreated}
          onBack={() => handleBack("workspace")}
        />
      )}
      {step === "runtime" && workspace && (
        runtimeInstructions ? (
          <StepPlatformFork
            wsId={workspace.id}
            onNext={handleRuntimeNext}
            onBack={() => handleBack("runtime")}
            cliInstructions={runtimeInstructions}
          />
        ) : (
          <StepRuntimeConnect
            wsId={workspace.id}
            onNext={handleRuntimeNext}
            onBack={() => handleBack("runtime")}
          />
        )
      )}
      {step === "agent" && runtime && (
        <StepAgent
          runtime={runtime}
          onCreated={handleAgentCreated}
          onSkip={handleAgentSkip}
          onBack={() => handleBack("agent")}
        />
      )}
      {step === "first_issue" && workspace && (
        <StepFirstIssue
          agent={agent}
          workspace={workspace}
          questionnaire={storedQuestionnaire}
          userName={user.name || user.email}
          userId={user.id}
          onDone={handleBootstrapDone}
          onSkip={handleBootstrapSkip}
        />
      )}
    </div>
  );
}

export type { OnboardingStep };
