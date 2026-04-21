"use client";

import type { Workspace } from "@multica/core/types";
import { CreateWorkspaceForm } from "../../workspace/create-workspace-form";

export function StepWorkspace({
  onCreated,
}: {
  onCreated: (workspace: Workspace) => void;
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
    </div>
  );
}
