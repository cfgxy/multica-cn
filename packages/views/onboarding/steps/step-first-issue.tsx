"use client";

import { useEffect, useRef, useState } from "react";
import { Loader2, AlertCircle } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import type { QuestionnaireAnswers } from "@multica/core/onboarding";
import type { Agent, Workspace } from "@multica/core/types";
import { runOnboardingBootstrap } from "../utils/bootstrap";

type Phase =
  | { status: "running" }
  | { status: "done"; firstIssueId: string | null; projectId: string | null }
  | { status: "error"; message: string };

export function StepFirstIssue({
  agent,
  workspace,
  questionnaire,
  userName,
  onDone,
  onSkip,
}: {
  agent: Agent | null;
  workspace: Workspace;
  questionnaire: QuestionnaireAnswers;
  userName: string;
  onDone: (firstIssueId: string | null, projectId: string | null) => void;
  onSkip: () => void;
}) {
  const [phase, setPhase] = useState<Phase>({ status: "running" });
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;

    runOnboardingBootstrap({ agent, workspace, questionnaire, userName })
      .then((result) => {
        setPhase({
          status: "done",
          firstIssueId: result.firstIssueId,
          projectId: result.projectId,
        });
      })
      .catch((err) => {
        setPhase({
          status: "error",
          message:
            err instanceof Error ? err.message : "Failed to set up your workspace",
        });
      });
  }, [agent, workspace, questionnaire, userName]);

  useEffect(() => {
    if (phase.status === "done") {
      onDone(phase.firstIssueId, phase.projectId);
    }
  }, [phase, onDone]);

  const retry = () => {
    started.current = false;
    setPhase({ status: "running" });
  };

  if (phase.status === "error") {
    return (
      <div className="flex w-full flex-col items-center gap-6 text-center">
        <div className="flex h-12 w-12 items-center justify-center rounded-full bg-destructive/10 text-destructive">
          <AlertCircle className="h-6 w-6" />
        </div>
        <div className="flex flex-col gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">
            Something went wrong
          </h1>
          <p className="text-sm text-muted-foreground">{phase.message}</p>
        </div>
        <div className="flex gap-3">
          <Button variant="outline" onClick={onSkip}>
            Continue anyway
          </Button>
          <Button onClick={retry}>Retry</Button>
        </div>
      </div>
    );
  }

  const subtitle = agent
    ? `Creating ${agent.name}, your first task, and a starter project...`
    : "Creating your starter project and a few things to try...";

  return (
    <div className="flex w-full flex-col items-center gap-6 text-center">
      <Loader2 className="h-10 w-10 animate-spin text-primary" />
      <div className="flex flex-col gap-2">
        <h1 className="text-2xl font-semibold tracking-tight">
          Setting up your workspace
        </h1>
        <p className="text-sm text-muted-foreground">{subtitle}</p>
      </div>
    </div>
  );
}
