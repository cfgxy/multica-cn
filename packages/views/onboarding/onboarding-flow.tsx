"use client";

import { useCallback, useState } from "react";
import { toast } from "sonner";
import { setCurrentWorkspace } from "@multica/core/platform";
import { useAuthStore } from "@multica/core/auth";
import {
  useOnboardingStore,
  type OnboardingStep,
  type QuestionnaireAnswers,
} from "@multica/core/onboarding";
import type { Agent, AgentRuntime, Workspace } from "@multica/core/types";
import { StepHeader } from "./components/step-header";
import { StepWelcome } from "./steps/step-welcome";
import { StepQuestionnaire } from "./steps/step-questionnaire";
import { StepWorkspace } from "./steps/step-workspace";
import { StepRuntimeConnect } from "./steps/step-runtime-connect";
import { StepPlatformFork } from "./steps/step-platform-fork";
import { StepAgent } from "./steps/step-agent";
import { StepFirstIssue } from "./steps/step-first-issue";

function pickInitialStep(): OnboardingStep {
  return useOnboardingStore.getState().state.current_step ?? "welcome";
}

export function OnboardingFlow({
  onComplete,
  runtimeInstructions,
}: {
  onComplete: (workspace?: Workspace, firstIssueId?: string) => void;
  runtimeInstructions?: React.ReactNode;
}) {
  const [step, setStep] = useState<OnboardingStep>(pickInitialStep);
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [runtime, setRuntime] = useState<AgentRuntime | null>(null);
  const [agent, setAgent] = useState<Agent | null>(null);

  const storedQuestionnaire = useOnboardingStore(
    (s) => s.state.questionnaire,
  );
  const advance = useOnboardingStore((s) => s.advance);
  const complete = useOnboardingStore((s) => s.complete);
  const user = useAuthStore((s) => s.user);

  // OnboardingFlow is only rendered when the shell has resolved
  // `user` to a non-null value (web page guard, desktop overlay effect).
  // Callers that violate this surface an obvious error instead of
  // silently degrading (e.g. unassigned sub-issues).
  if (!user) {
    throw new Error("OnboardingFlow requires an authenticated user");
  }

  const runtimeWorkspace = workspace;

  const handleWelcomeNext = useCallback(() => {
    void advance({ current_step: "questionnaire" });
    setStep("questionnaire");
  }, [advance]);

  const handleQuestionnaireSubmit = useCallback(
    (answers: QuestionnaireAnswers) => {
      void advance({
        questionnaire: answers,
        current_step: "workspace",
      });
      setStep("workspace");
    },
    [advance],
  );

  const handleWorkspaceCreated = useCallback(
    (ws: Workspace) => {
      setWorkspace(ws);
      setCurrentWorkspace(ws.slug, ws.id);
      void advance({ current_step: "runtime" });
      setStep("runtime");
    },
    [advance],
  );

  const handleRuntimeNext = useCallback(
    (rt: AgentRuntime | null) => {
      setRuntime(rt);
      // No runtime → no agent possible; converge to first_issue step
      // with agent=null and let bootstrap run the self-serve path.
      const next: OnboardingStep = rt ? "agent" : "first_issue";
      void advance({ current_step: next });
      setStep(next);
    },
    [advance],
  );

  const handleAgentCreated = useCallback(
    (created: Agent) => {
      setAgent(created);
      void advance({ current_step: "first_issue" });
      setStep("first_issue");
    },
    [advance],
  );

  const handleAgentSkip = useCallback(() => {
    void advance({ current_step: "first_issue" });
    setStep("first_issue");
  }, [advance]);

  // complete() is idempotent server-side (COALESCE on onboarded_at), so a
  // failed call surfaces a toast and stays on the current step for retry.
  // Letting the error bubble would hit the React error boundary with no
  // recovery path.
  const handleBootstrapDone = useCallback(
    async (firstIssueId: string | null) => {
      try {
        await complete();
      } catch (err) {
        toast.error(
          err instanceof Error ? err.message : "Failed to finish onboarding",
        );
        return;
      }
      onComplete(workspace ?? undefined, firstIssueId ?? undefined);
    },
    [complete, workspace, onComplete],
  );

  const handleBootstrapSkip = useCallback(async () => {
    try {
      await complete();
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to finish onboarding",
      );
      return;
    }
    onComplete(workspace ?? undefined);
  }, [complete, workspace, onComplete]);

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
        <StepWorkspace onCreated={handleWorkspaceCreated} />
      )}
      {step === "runtime" && runtimeWorkspace && (
        runtimeInstructions ? (
          <StepPlatformFork
            wsId={runtimeWorkspace.id}
            onNext={handleRuntimeNext}
            cliInstructions={runtimeInstructions}
          />
        ) : (
          <StepRuntimeConnect
            wsId={runtimeWorkspace.id}
            onNext={handleRuntimeNext}
          />
        )
      )}
      {step === "agent" && runtime && (
        <StepAgent
          runtime={runtime}
          onCreated={handleAgentCreated}
          onSkip={handleAgentSkip}
        />
      )}
      {step === "first_issue" && runtimeWorkspace && (
        <StepFirstIssue
          agent={agent}
          workspace={runtimeWorkspace}
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
