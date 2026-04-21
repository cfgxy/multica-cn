"use client";

import { Check } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import type { Agent } from "@multica/core/types";

/**
 * Final onboarding step. Two copy variants depending on whether an agent
 * was created:
 *  - Agent created: celebratory, names the agent
 *  - Skipped: lightweight, points to where to come back
 *
 * Single primary CTA that exits the overlay and drops the user into
 * their workspace.
 */
export function StepComplete({
  agent,
  onFinish,
}: {
  agent: Agent | null;
  onFinish: () => void;
}) {
  return (
    <div className="flex w-full max-w-md flex-col items-center gap-6 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-success/10 text-success">
        <Check className="h-6 w-6" />
      </div>

      <div className="flex flex-col gap-3">
        <h1 className="text-3xl font-semibold tracking-tight">
          {agent ? "You're all set" : "You're in"}
        </h1>
        <p className="text-base text-muted-foreground">
          {agent ? (
            <>
              <span className="font-medium text-foreground">{agent.name}</span>{" "}
              is ready to pick up tasks in your workspace.
            </>
          ) : (
            <>
              You can create agents anytime from the Agents page. Your
              workspace is ready when you are.
            </>
          )}
        </p>
      </div>

      <Button size="lg" onClick={onFinish}>
        Enter your workspace
      </Button>
    </div>
  );
}
