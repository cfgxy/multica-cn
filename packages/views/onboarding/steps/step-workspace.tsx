"use client";

import { Button } from "@multica/ui/components/ui/button";
import type { Workspace } from "@multica/core/types";
import { CreateWorkspaceForm } from "../../workspace/create-workspace-form";

/**
 * Onboarding step that guides workspace creation. Distinct from
 * `NewWorkspacePage` (which is the standalone /workspaces/new transition)
 * — onboarding is a guided flow with a different voice. Reuses
 * `CreateWorkspaceForm` so the form logic (slug generation, conflict
 * handling, validation) stays single-sourced.
 *
 * `onSkip` is present for debug / QA: during active development we want
 * to walk through the full flow against any account, including those
 * that already have a workspace. When the product decides to truly force
 * creation, `onSkip` disappears and the button goes with it.
 */
export function StepWorkspace({
  onCreated,
  onSkip,
}: {
  onCreated: (workspace: Workspace) => void;
  onSkip?: () => void;
}) {
  return (
    <div className="flex w-full max-w-md flex-col items-center gap-6">
      <div className="flex flex-col gap-3 text-center">
        <h1 className="text-3xl font-semibold tracking-tight">
          Create your first workspace
        </h1>
        <p className="text-base text-muted-foreground">
          A workspace is your home for issues, agents, and teammates.
          You can invite your team once it's set up.
        </p>
      </div>
      <CreateWorkspaceForm onSuccess={onCreated} />
      {onSkip && (
        <Button
          variant="ghost"
          size="sm"
          className="text-muted-foreground"
          onClick={onSkip}
        >
          Skip for now
        </Button>
      )}
    </div>
  );
}
