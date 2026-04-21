"use client";

import { Button } from "@multica/ui/components/ui/button";

/**
 * First step of the onboarding flow. Currently a placeholder — real copy
 * and branding land in a later pass. The shape is stable: a title, a short
 * description, and a single primary CTA that advances the flow.
 */
export function StepWelcome({ onNext }: { onNext: () => void }) {
  return (
    <div className="flex w-full max-w-md flex-col items-center gap-6 text-center">
      <div className="flex flex-col gap-3">
        <h1 className="text-3xl font-semibold tracking-tight">
          Welcome to Multica
        </h1>
        <p className="text-base text-muted-foreground">
          Where you and your AI agents ship work together. Let's get your
          space set up — it takes about a minute.
        </p>
      </div>
      <Button size="lg" onClick={onNext}>
        Get started
      </Button>
    </div>
  );
}
