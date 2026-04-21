"use client";

import { useCallback, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { setCurrentWorkspace } from "@multica/core/platform";
import { useAuthStore } from "@multica/core/auth";
import {
  useOnboardingStore,
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

export type OnboardingStep =
  | "welcome"
  | "questionnaire"
  | "workspace"
  | "runtime"
  | "agent"
  | "first_issue";

function pickInitialStep(): OnboardingStep {
  const s = useOnboardingStore.getState().state;
  const pristine =
    s.current_step === "questionnaire" &&
    s.questionnaire.team_size === null &&
    s.questionnaire.role === null &&
    s.questionnaire.use_case === null;
  if (pristine) return "welcome";
  return s.current_step ?? "welcome";
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

  const { data: workspaces = [] } = useQuery(workspaceListOptions());
  const runtimeWorkspace = workspace ?? workspaces[0] ?? null;

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

  const handleWorkspaceCreated = useCallback((ws: Workspace) => {
    setWorkspace(ws);
    setCurrentWorkspace(ws.slug, ws.id);
    setStep("runtime");
  }, []);

  const handleWorkspaceSkip = useCallback(() => {
    setStep("runtime");
  }, []);

  const handleRuntimeNext = useCallback((rt: AgentRuntime | null) => {
    setRuntime(rt);
    // No runtime → no agent possible. Converge into first_issue step
    // with agent=null; bootstrap runs the self-serve path.
    if (!rt) {
      setStep("first_issue");
      return;
    }
    setStep("agent");
  }, []);

  const handleAgentCreated = useCallback((created: Agent) => {
    setAgent(created);
    setStep("first_issue");
  }, []);

  const handleAgentSkip = useCallback(() => {
    // Same convergence point as no-runtime: first_issue step,
    // bootstrap runs self-serve path.
    setStep("first_issue");
  }, []);

  const handleBootstrapDone = useCallback(
    async (firstIssueId: string | null, projectId: string | null) => {
      await complete({
        ...(firstIssueId ? { first_issue_id: firstIssueId } : {}),
        ...(projectId ? { onboarding_project_id: projectId } : {}),
      });
      onComplete(workspace ?? undefined, firstIssueId ?? undefined);
    },
    [complete, workspace, onComplete],
  );

  const handleBootstrapSkip = useCallback(async () => {
    await complete({});
    onComplete(workspace ?? undefined);
  }, [complete, workspace, onComplete]);

  const handleWaitlist = useCallback(
    (email: string, description: string | null) => {
      // Persist waitlist fields now; let bootstrap + complete fire
      // in the next step, same as every other no-agent path.
      void advance({
        cloud_waitlist_email: email,
        cloud_waitlist_description: description,
      });
      setStep("first_issue");
    },
    [advance],
  );

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
          onCreated={handleWorkspaceCreated}
          onSkip={
            workspaces.length > 0 ? handleWorkspaceSkip : undefined
          }
        />
      )}
      {step === "runtime" && runtimeWorkspace && (
        runtimeInstructions ? (
          <StepPlatformFork
            wsId={runtimeWorkspace.id}
            onNext={handleRuntimeNext}
            onWaitlist={handleWaitlist}
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
          userName={user?.name ?? user?.email ?? ""}
          onDone={handleBootstrapDone}
          onSkip={handleBootstrapSkip}
        />
      )}
    </div>
  );
}
