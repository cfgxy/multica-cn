"use client";

import { ArrowLeft } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import type { Workspace } from "@multica/core/types";
import { CreateWorkspaceForm } from "../../workspace/create-workspace-form";

export function StepWorkspace({
  existing,
  onCreated,
  onBack,
}: {
  /**
   * If the user already has a workspace (common on resume after Step 2
   * was completed but Step 3+ wasn't), render a short confirmation
   * instead of the create form. Eliminates the slug-conflict dead-end
   * the form would hit otherwise.
   */
  existing?: Workspace | null;
  onCreated: (workspace: Workspace) => void | Promise<void>;
  onBack?: () => void;
}) {
  const reusing = existing ?? null;
  return (
    <div className="flex w-full flex-col gap-4">
      {onBack && (
        <button
          type="button"
          onClick={onBack}
          className="flex items-center gap-1.5 self-start text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back
        </button>
      )}
      <div className="flex w-full max-w-md flex-col items-center gap-6 self-center">
        {reusing ? (
          <>
            <div className="flex flex-col gap-3 text-center">
              <h1 className="text-3xl font-semibold tracking-tight">
                Continue with {reusing.name}
              </h1>
              <p className="text-base text-muted-foreground">
                You already have a workspace. Let&apos;s continue setting
                it up.
              </p>
            </div>
            <Button size="lg" onClick={() => onCreated(reusing)}>
              Continue
            </Button>
          </>
        ) : (
          <>
            <div className="flex flex-col gap-3 text-center">
              <h1 className="text-3xl font-semibold tracking-tight">
                Create your first workspace
              </h1>
              <p className="text-base text-muted-foreground">
                A workspace is your home for issues, agents, and teammates.
                You can invite your team once it&apos;s set up.
              </p>
            </div>
            <CreateWorkspaceForm onSuccess={onCreated} />
          </>
        )}
      </div>
    </div>
  );
}
