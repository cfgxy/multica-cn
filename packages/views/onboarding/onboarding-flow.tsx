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
    // No runtime → can't build CreateAgentRequest; skip to finish.
    if (!rt) {
      void complete({});
      onComplete(workspace ?? undefined);
      return;
    }
    setStep("agent");
  }, [complete, workspace, onComplete]);

  const handleAgentCreated = useCallback((created: Agent) => {
    setAgent(created);
    setStep("first_issue");
  }, []);

  const handleAgentSkip = useCallback(async () => {
    // No agent → no aha-moment task to create; just finish onboarding.
    await complete({});
    onComplete(workspace ?? undefined);
  }, [complete, workspace, onComplete]);

  const handleBootstrapDone = useCallback(
    async (firstIssueId: string, projectId: string | null) => {
      await complete({
        first_issue_id: firstIssueId,
        onboarding_project_id: projectId ?? undefined,
      });
      onComplete(workspace ?? undefined, firstIssueId);
    },
    [complete, workspace, onComplete],
  );

  const handleBootstrapSkip = useCallback(async () => {
    await complete({});
    onComplete(workspace ?? undefined);
  }, [complete, workspace, onComplete]);

  const handleWaitlist = useCallback(
    async (email: string, description: string | null) => {
      await advance({
        cloud_waitlist_email: email,
        cloud_waitlist_description: description,
      });
      await complete({});
      onComplete(workspace ?? undefined);
    },
    [advance, complete, workspace, onComplete],
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
      {step === "first_issue" && agent && runtimeWorkspace && (
        <StepFirstIssue
          agent={agent}
          workspace={runtimeWorkspace}
          questionnaire={storedQuestionnaire}
          userName={user?.name ?? user?.email ?? ""}
          agentName={agent.name}
          onDone={handleBootstrapDone}
          onSkip={handleBootstrapSkip}
        />
      )}
    </div>
  );
}
