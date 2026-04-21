"use client";

import { useCallback, useRef, useState } from "react";
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

function isOnboardingStep(value: unknown): value is OnboardingStep {
  return (
    value === "welcome" ||
    (ONBOARDING_STEP_ORDER as readonly string[]).includes(value as string)
  );
}

function pickInitialStep(currentStep: string | null): OnboardingStep {
  if (isOnboardingStep(currentStep)) return currentStep;
  return "welcome";
}

function mergeQuestionnaire(
  raw: Record<string, unknown>,
): QuestionnaireAnswers {
  return { ...EMPTY_QUESTIONNAIRE, ...(raw as Partial<QuestionnaireAnswers>) };
}

/**
 * Shell's onComplete contract:
 *   onComplete(workspace?, firstIssueId?) — if workspace + firstIssueId
 *   are both supplied, navigate to the issue detail; if only workspace,
 *   its issues list; if neither, fall back to root.
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

  const storedQuestionnaire = mergeQuestionnaire(user.onboarding_questionnaire);

  const [step, setStep] = useState<OnboardingStep>(() =>
    pickInitialStep(user.onboarding_current_step),
  );

  // `furthestStep` is the server's `onboarding_current_step` view —
  // "the furthest point the user has ever reached". `step` is the
  // locally-rendered step, which can differ when the user clicks
  // Back to edit an earlier answer. Submitting an edit advances
  // `furthestStep` only if it exceeds the previous max, and sends
  // the user back to `furthestStep` on completion so the edit
  // doesn't cost them their progress.
  const furthestStepRef = useRef<OnboardingStep>(step);

  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [runtime, setRuntime] = useState<AgentRuntime | null>(null);
  const [agent, setAgent] = useState<Agent | null>(null);

  const runtimeWorkspace = workspace;

  // Advance `furthestStepRef` monotonically. Returns the step to
  // actually move the user to after a submit: either the next step
  // in the canonical order (first pass) or the previous furthest
  // (edit mode).
  const resolveNextStep = useCallback(
    (localStep: OnboardingStep, intendedNext: OnboardingStep): OnboardingStep => {
      const furthestIdx = ONBOARDING_STEP_ORDER.indexOf(furthestStepRef.current);
      const localIdx = ONBOARDING_STEP_ORDER.indexOf(localStep);
      // If the user is editing an earlier step, bounce them back to the
      // furthest reached step rather than re-walking downstream steps.
      if (localIdx >= 0 && localIdx < furthestIdx) {
        return furthestStepRef.current;
      }
      const intendedIdx = ONBOARDING_STEP_ORDER.indexOf(intendedNext);
      if (intendedIdx > furthestIdx) {
        furthestStepRef.current = intendedNext;
      }
      return intendedNext;
    },
    [],
  );

  const handleWelcomeNext = useCallback(async () => {
    await advanceOnboarding({ current_step: "questionnaire" });
    furthestStepRef.current = "questionnaire";
    setStep("questionnaire");
  }, []);

  const handleQuestionnaireSubmit = useCallback(
    async (answers: QuestionnaireAnswers) => {
      const nextStep = resolveNextStep("questionnaire", "workspace");
      // In edit mode we don't regress current_step on the server — only
      // save the questionnaire changes. In first-pass we advance both.
      const patch: Parameters<typeof advanceOnboarding>[0] =
        nextStep === "workspace"
          ? { current_step: "workspace", questionnaire: answers }
          : { questionnaire: answers };
      await advanceOnboarding(patch);
      setStep(nextStep);
    },
    [resolveNextStep],
  );

  const handleWorkspaceCreated = useCallback(
    async (ws: Workspace) => {
      setWorkspace(ws);
      setCurrentWorkspace(ws.slug, ws.id);
      const nextStep = resolveNextStep("workspace", "runtime");
      if (nextStep === "runtime") {
        await advanceOnboarding({ current_step: "runtime" });
      }
      setStep(nextStep);
    },
    [resolveNextStep],
  );

  const handleRuntimeNext = useCallback(
    async (rt: AgentRuntime | null) => {
      setRuntime(rt);
      const intended: OnboardingStep = rt ? "agent" : "first_issue";
      const nextStep = resolveNextStep("runtime", intended);
      if (nextStep === intended) {
        await advanceOnboarding({ current_step: intended });
      }
      setStep(nextStep);
    },
    [resolveNextStep],
  );

  const handleAgentCreated = useCallback(
    async (created: Agent) => {
      setAgent(created);
      const nextStep = resolveNextStep("agent", "first_issue");
      if (nextStep === "first_issue") {
        await advanceOnboarding({ current_step: "first_issue" });
      }
      setStep(nextStep);
    },
    [resolveNextStep],
  );

  const handleAgentSkip = useCallback(async () => {
    const nextStep = resolveNextStep("agent", "first_issue");
    if (nextStep === "first_issue") {
      await advanceOnboarding({ current_step: "first_issue" });
    }
    setStep(nextStep);
  }, [resolveNextStep]);

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
          onCreated={handleWorkspaceCreated}
          onBack={() => handleBack("workspace")}
        />
      )}
      {step === "runtime" && runtimeWorkspace && (
        runtimeInstructions ? (
          <StepPlatformFork
            wsId={runtimeWorkspace.id}
            onNext={handleRuntimeNext}
            onBack={() => handleBack("runtime")}
            cliInstructions={runtimeInstructions}
          />
        ) : (
          <StepRuntimeConnect
            wsId={runtimeWorkspace.id}
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
